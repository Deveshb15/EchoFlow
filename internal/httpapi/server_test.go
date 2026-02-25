package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"echoflow/internal/config"
	"echoflow/internal/pipeline"
	"echoflow/internal/postprocess"
)

type stubTranscription struct {
	text     string
	err      error
	fileBody string
	model    string
}

func (s *stubTranscription) Transcribe(_ context.Context, file io.Reader, _ string, model string) (string, error) {
	body, _ := io.ReadAll(file)
	s.fileBody = string(body)
	s.model = model
	return s.text, s.err
}

type stubPostProcess struct {
	result postprocess.Result
	err    error
	input  postprocess.Input
}

func (s *stubPostProcess) Process(_ context.Context, in postprocess.Input) (postprocess.Result, error) {
	s.input = in
	return s.result, s.err
}

type stubPipeline struct {
	result   pipeline.ProcessResult
	err      error
	input    pipeline.ProcessInput
	fileBody string
}

func (s *stubPipeline) Process(_ context.Context, in pipeline.ProcessInput) (pipeline.ProcessResult, error) {
	s.input = in
	body, _ := io.ReadAll(in.File)
	s.fileBody = string(body)
	return s.result, s.err
}

type stubUpstream struct{ err error }

func (s stubUpstream) CheckModels(context.Context) error { return s.err }

func newTestHandler(t *testing.T, deps Dependencies) http.Handler {
	t.Helper()
	cfg := config.Config{
		MaxUploadBytes:  1024 * 1024,
		UpstreamAPIKey:  "x",
		UpstreamBaseURL: "http://example.com",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(cfg, logger, deps)
}

func TestHealthz(t *testing.T) {
	h := newTestHandler(t, Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   &stubPostProcess{},
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestPostProcessHandlerReturnsUsageAndNoPrompt(t *testing.T) {
	pp := &stubPostProcess{result: postprocess.Result{
		Transcript: "cleaned",
		Usage: &postprocess.TokenUsage{
			PromptTokens:     40,
			CompletionTokens: 8,
			TotalTokens:      48,
		},
	}}
	h := newTestHandler(t, Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   pp,
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{},
	})

	payload := map[string]any{
		"transcript":           "raw",
		"context_summary":      "replying to email",
		"include_debug_prompt": true,
		"custom_vocabulary":    "Alice",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/post-process", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
	if pp.input.Transcript != "raw" || !pp.input.IncludeDebugPrompt {
		t.Fatalf("unexpected post-process input: %+v", pp.input)
	}
	if !strings.Contains(w.Body.String(), `"usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}`) {
		t.Fatalf("expected usage in body: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"prompt"`) {
		t.Fatalf("prompt field should not be returned: %s", w.Body.String())
	}
}

func TestTranscriptionsHandlerMultipart(t *testing.T) {
	tr := &stubTranscription{text: "hello"}
	h := newTestHandler(t, Dependencies{
		Transcription: tr,
		PostProcess:   &stubPostProcess{},
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{},
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", "whisper-large-v3")
	part, _ := mw.CreateFormFile("file", "sample.wav")
	_, _ = part.Write([]byte("audio-bytes"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/transcriptions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
	if tr.fileBody != "audio-bytes" {
		t.Fatalf("unexpected file body: %q", tr.fileBody)
	}
	if tr.model != "whisper-large-v3" {
		t.Fatalf("unexpected model: %q", tr.model)
	}
}

func TestPipelineHandlerReturnsUsageAndNoPrompt(t *testing.T) {
	pipe := &stubPipeline{result: pipeline.ProcessResult{
		RawTranscript:        "raw",
		FinalTranscript:      "final",
		PostProcessingStatus: "Post-processing succeeded",
		PostProcessingUsage: &postprocess.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 20,
			TotalTokens:      120,
		},
	}}
	h := newTestHandler(t, Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   &stubPostProcess{},
		Pipeline:      pipe,
		Upstream:      stubUpstream{},
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("context_summary", "email reply")
	_ = mw.WriteField("include_debug", "true")
	_ = mw.WriteField("custom_vocabulary", "Alice")
	part, _ := mw.CreateFormFile("file", "sample.wav")
	_, _ = part.Write([]byte("audio-payload"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/pipeline/process", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
	if pipe.fileBody != "audio-payload" {
		t.Fatalf("unexpected file body: %q", pipe.fileBody)
	}
	if !pipe.input.IncludeDebug {
		t.Fatal("expected include_debug to be parsed")
	}
	if pipe.input.ContextSummary != "email reply" {
		t.Fatalf("unexpected context summary: %q", pipe.input.ContextSummary)
	}
	if !strings.Contains(w.Body.String(), `"post_processing_usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}`) {
		t.Fatalf("expected post_processing_usage in body: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"post_processing_prompt"`) {
		t.Fatalf("post_processing_prompt should not be returned: %s", w.Body.String())
	}
}

func TestBYOTRequiredWhenNoServerAPIKey(t *testing.T) {
	h := NewServer(config.Config{
		MaxUploadBytes:  1024 * 1024,
		UpstreamBaseURL: "http://example.com",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   &stubPostProcess{},
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/post-process", strings.NewReader(`{"transcript":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Groq Cloud bearer token") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestBYOTAuthorizationHeaderAcceptedWhenNoServerAPIKey(t *testing.T) {
	pp := &stubPostProcess{result: postprocess.Result{Transcript: "cleaned"}}
	h := NewServer(config.Config{
		MaxUploadBytes:  1024 * 1024,
		UpstreamBaseURL: "http://example.com",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   pp,
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/post-process", strings.NewReader(`{"transcript":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer groq_test_token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
}

func TestReadyzSkipsUpstreamCheckWithoutAnyToken(t *testing.T) {
	h := NewServer(config.Config{
		MaxUploadBytes:  1024 * 1024,
		UpstreamBaseURL: "http://example.com",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Transcription: &stubTranscription{},
		PostProcess:   &stubPostProcess{},
		Pipeline:      &stubPipeline{},
		Upstream:      stubUpstream{err: io.EOF},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", w.Code, w.Body.String())
	}
}
