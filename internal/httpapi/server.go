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

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
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

type MetricsObserver interface {
	ObserveHTTP(route, method string, status int, duration time.Duration)
	IncPipelineFallback()
}

type Dependencies struct {
	Transcription  TranscriptionService
	PostProcess    PostProcessService
	Pipeline       PipelineService
	Upstream       UpstreamChecker
	Metrics        MetricsObserver
	MetricsHandler http.Handler
}

type server struct {
	cfg          config.Config
	logger       *slog.Logger
	transcriber  TranscriptionService
	postProcess  PostProcessService
	pipeline     PipelineService
	upstream     UpstreamChecker
	metrics      MetricsObserver
	metricsRoute http.Handler
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
		cfg:          cfg,
		logger:       logger,
		transcriber:  deps.Transcription,
		postProcess:  deps.PostProcess,
		pipeline:     deps.Pipeline,
		upstream:     deps.Upstream,
		metrics:      deps.Metrics,
		metricsRoute: deps.MetricsHandler,
	}

	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	})

	r.Use(s.requestIDMiddleware)
	r.Use(s.loggingMiddleware)
	r.Use(s.recoverMiddleware)
	r.Use(s.authMiddleware)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	if s.metricsRoute != nil {
		r.Handle("/metrics", s.metricsRoute)
	}

	r.Route("/v1", func(r chi.Router) {
		r.Post("/transcriptions", s.handleTranscriptions)
		r.Post("/post-process", s.handlePostProcess)
		r.Post("/pipeline/process", s.handlePipelineProcess)
	})

	return r
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.HealthResponse{OK: true})
}

func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.cfg.UpstreamAPIKey == "" && openai.RequestAPIKeyFromContext(r.Context()) == "" {
		writeJSON(w, http.StatusOK, model.ReadyResponse{OK: true, ServiceName: "EchoFlow"})
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
	file, header, form, err := s.readMultipartAudio(w, r)
	if err != nil {
		s.handleMultipartReadError(w, r, err)
		return
	}
	defer cleanupMultipartForm(form)
	defer func() { _ = file.Close() }()

	text, err := s.transcriber.Transcribe(r.Context(), file, header.Filename, strings.TrimSpace(r.FormValue("model")))
	if err != nil {
		s.writeMappedError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, model.TranscriptionResponse{Text: text})
}

func (s *server) handlePostProcess(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer func() { _ = r.Body.Close() }()

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
	file, header, form, err := s.readMultipartAudio(w, r)
	if err != nil {
		s.handleMultipartReadError(w, r, err)
		return
	}
	defer cleanupMultipartForm(form)
	defer func() { _ = file.Close() }()

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
	if s.metrics != nil && result.PostProcessingStatus == "Post-processing failed, using raw transcript" {
		s.metrics.IncPipelineFallback()
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
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}

		route := r.URL.Path
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if pattern := rctx.RoutePattern(); pattern != "" {
				route = pattern
			}
		}

		duration := time.Since(started)
		if s.metrics != nil {
			s.metrics.ObserveHTTP(route, r.Method, status, duration)
		}

		s.logger.Info("http_request",
			"request_id", requestIDFromContext(r.Context()),
			"method", r.Method,
			"route", route,
			"path", r.URL.Path,
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", duration.Milliseconds(),
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, hasHeader, ok := extractBearerToken(r.Header.Get("Authorization"))
		if hasHeader && !ok {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authorization must be Bearer <groq_cloud_token>", nil)
			return
		}
		if !isPublicPath(r.URL.Path) && token == "" && s.cfg.UpstreamAPIKey == "" {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing Groq Cloud bearer token", nil)
			return
		}
		if token != "" {
			r = r.WithContext(openai.WithRequestAPIKey(r.Context(), token))
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicPath(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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

func extractBearerToken(header string) (token string, hasHeader bool, ok bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false, true
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", true, false
	}
	token = strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", true, false
	}
	return token, true, true
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
