package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"echoflow/internal/config"
	"echoflow/internal/model"
	"echoflow/internal/pipeline"
	"echoflow/internal/postprocess"
	"echoflow/internal/upstream/openai"
)

type TranscriptionService interface {
	Transcribe(ctx context.Context, file io.Reader, fileName, model string) (string, error)
}

type PostProcessService interface {
	Process(ctx context.Context, in postprocess.Input) (postprocess.Result, error)
}

type PipelineService interface {
	Process(ctx context.Context, in pipeline.ProcessInput) (pipeline.ProcessResult, error)
}

type UpstreamChecker interface {
	CheckModels(ctx context.Context) error
}

type Dependencies struct {
	Transcription TranscriptionService
	PostProcess   PostProcessService
	Pipeline      PipelineService
	Upstream      UpstreamChecker
}

type server struct {
	cfg         config.Config
	logger      *slog.Logger
	transcriber TranscriptionService
	postProcess PostProcessService
	pipeline    PipelineService
	upstream    UpstreamChecker
}

type ctxKey string

const (
	requestIDHeader  = "X-Request-Id"
	requestIDContext = ctxKey("request_id")
	maxJSONBodyBytes = 1 << 20
)

func NewServer(cfg config.Config, logger *slog.Logger, deps Dependencies) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if deps.Transcription == nil || deps.PostProcess == nil || deps.Pipeline == nil || deps.Upstream == nil {
		panic("httpapi: all dependencies are required")
	}

	s := &server{
		cfg:         cfg,
		logger:      logger,
		transcriber: deps.Transcription,
		postProcess: deps.PostProcess,
		pipeline:    deps.Pipeline,
		upstream:    deps.Upstream,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/v1/transcriptions", s.handleTranscriptions)
	mux.HandleFunc("/v1/post-process", s.handlePostProcess)
	mux.HandleFunc("/v1/pipeline/process", s.handlePipelineProcess)

	var handler http.Handler = mux
	handler = s.authMiddleware(handler)
	handler = s.recoverMiddleware(handler)
	handler = s.loggingMiddleware(handler)
	handler = s.requestIDMiddleware(handler)
	return handler
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(s, w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, model.HealthResponse{OK: true})
}

func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(s, w, r, http.MethodGet) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.upstream.CheckModels(ctx); err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "not_ready", "upstream check failed", detailsForError(err))
		return
	}
	writeJSON(w, http.StatusOK, model.ReadyResponse{OK: true, ServiceName: "EchoFlow"})
}

func (s *server) handleTranscriptions(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(s, w, r, http.MethodPost) {
		return
	}

	file, header, form, err := s.readMultipartAudio(w, r)
	if err != nil {
		s.handleMultipartReadError(w, r, err)
		return
	}
	defer cleanupMultipartForm(form)
	defer file.Close()

	text, err := s.transcriber.Transcribe(r.Context(), file, header.Filename, strings.TrimSpace(r.FormValue("model")))
	if err != nil {
		s.writeMappedError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, model.TranscriptionResponse{Text: text})
}

func (s *server) handlePostProcess(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(s, w, r, http.MethodPost) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()

	var req model.PostProcessRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.handleJSONDecodeError(w, r, err)
		return
	}
	if err := ensureBodyFullyConsumed(decoder); err != nil {
		s.handleJSONDecodeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.Transcript) == "" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "transcript is required", nil)
		return
	}

	result, err := s.postProcess.Process(r.Context(), postprocess.Input{
		Transcript:         req.Transcript,
		ContextSummary:     req.ContextSummary,
		CustomVocabulary:   req.CustomVocabulary,
		CustomSystemPrompt: req.CustomSystemPrompt,
		Model:              req.Model,
		IncludeDebugPrompt: req.IncludeDebugPrompt,
	})
	if err != nil {
		s.writeMappedError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, model.PostProcessResponse{
		Transcript: result.Transcript,
		Status:     "post-processing succeeded",
		Usage:      toModelTokenUsage(result.Usage),
	})
}

func (s *server) handlePipelineProcess(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(s, w, r, http.MethodPost) {
		return
	}

	file, header, form, err := s.readMultipartAudio(w, r)
	if err != nil {
		s.handleMultipartReadError(w, r, err)
		return
	}
	defer cleanupMultipartForm(form)
	defer file.Close()

	includeDebug, err := parseOptionalBool(r.FormValue("include_debug"))
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "include_debug must be a boolean", nil)
		return
	}

	result, err := s.pipeline.Process(r.Context(), pipeline.ProcessInput{
		File:               file,
		FileName:           header.Filename,
		ContextSummary:     r.FormValue("context_summary"),
		CustomVocabulary:   r.FormValue("custom_vocabulary"),
		CustomSystemPrompt: r.FormValue("custom_system_prompt"),
		TranscriptionModel: r.FormValue("transcription_model"),
		PostProcessModel:   r.FormValue("post_process_model"),
		IncludeDebug:       includeDebug,
	})
	if err != nil {
		s.writeMappedError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, model.PipelineProcessResponse{
		RawTranscript:        result.RawTranscript,
		FinalTranscript:      result.FinalTranscript,
		PostProcessingStatus: result.PostProcessingStatus,
		PostProcessingUsage:  toModelTokenUsage(result.PostProcessingUsage),
		TimingsMS: model.PipelineTimings{
			Transcription:  result.Timings.Transcription.Milliseconds(),
			PostProcessing: result.Timings.PostProcessing.Milliseconds(),
			Total:          result.Timings.Total.Milliseconds(),
		},
	})
}

func (s *server) readMultipartAudio(w http.ResponseWriter, r *http.Request) (multipart.File, *multipart.FileHeader, *multipart.Form, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	if err := r.ParseMultipartForm(minInt64(s.cfg.MaxUploadBytes, 8<<20)); err != nil {
		return nil, nil, nil, err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, r.MultipartForm, err
	}
	return file, header, r.MultipartForm, nil
}

func (s *server) handleMultipartReadError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		s.writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("request exceeds %d bytes", s.cfg.MaxUploadBytes), nil)
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "no such file") || strings.Contains(strings.ToLower(err.Error()), "missing") {
		s.writeError(w, r, http.StatusBadRequest, "invalid_request", "multipart field 'file' is required", nil)
		return
	}
	s.writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid multipart form data", nil)
}

func (s *server) handleJSONDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		s.writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "JSON body too large", nil)
		return
	}
	s.writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON body", nil)
}

func (s *server) writeMappedError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "request failed"
	details := detailsForError(err)

	var upstreamErr *openai.Error
	switch {
	case errors.As(err, &upstreamErr):
		status = http.StatusBadGateway
		code = "upstream_request_failed"
		message = "upstream request failed"
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		code = "timeout"
		message = "request timed out"
	case errors.Is(err, context.Canceled):
		status = 499
		code = "canceled"
		message = "request canceled"
	}

	if status == 499 {
		// Non-standard, but useful; if you prefer strict HTTP semantics change to 408.
		w.WriteHeader(499)
		_ = json.NewEncoder(w).Encode(model.ErrorResponse{
			Error:     model.APIError{Code: code, Message: message, Details: details},
			RequestID: requestIDFromContext(r.Context()),
		})
		return
	}

	s.writeError(w, r, status, code, message, details)
}

func (s *server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	if rid := requestIDFromContext(r.Context()); rid != "" {
		w.Header().Set(requestIDHeader, rid)
	}
	writeJSON(w, status, model.ErrorResponse{
		Error:     model.APIError{Code: code, Message: message, Details: details},
		RequestID: requestIDFromContext(r.Context()),
	})
}

func (s *server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDContext, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("http_request",
			"request_id", requestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic recovered", "request_id", requestIDFromContext(r.Context()), "panic", rec)
				s.writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) authMiddleware(next http.Handler) http.Handler {
	if !s.cfg.EnableAuth {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		expected := "Bearer " + s.cfg.APIBearerToken
		if authHeader != expected {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireMethod(s *server, w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	s.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ensureBodyFullyConsumed(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func parseOptionalBool(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

func cleanupMultipartForm(form *multipart.Form) {
	if form != nil {
		_ = form.RemoveAll()
	}
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContext).(string)
	return value
}

func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func toModelTokenUsage(u *postprocess.TokenUsage) *model.TokenUsage {
	if u == nil {
		return nil
	}
	return &model.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

func detailsForError(err error) map[string]any {
	if err == nil {
		return nil
	}
	details := map[string]any{"error": err.Error()}
	var upstreamErr *openai.Error
	if errors.As(err, &upstreamErr) {
		details["upstream_status"] = upstreamErr.StatusCode
		if upstreamErr.Body != "" {
			details["upstream_body"] = upstreamErr.Body
		}
	}
	return details
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}
