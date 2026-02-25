package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribeParsesJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		_ = r.MultipartForm.RemoveAll()
		if r.FormValue("model") != "whisper-large-v3" {
			t.Fatalf("unexpected model: %q", r.FormValue("model"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello"}`)
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", ts.Client())
	text, err := c.Transcribe(context.Background(), strings.NewReader("audio"), "sample.wav", "whisper-large-v3")
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if text != "hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestTranscribeParsesPlainTextResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello\nworld")
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", ts.Client())
	text, err := c.Transcribe(context.Background(), strings.NewReader("audio"), "sample.wav", "whisper-large-v3")
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if text != "hello world" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestChatCompletionParsesContentAndUsage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"cleaned"}}],"usage":{"prompt_tokens":50,"completion_tokens":10,"total_tokens":60}}`)
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", ts.Client())
	resp, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:       "m",
		Temperature: 0,
		Messages:    []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if resp.Content != "cleaned" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 60 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestTranscribeReturnsUpstreamError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", ts.Client())
	_, err := c.Transcribe(context.Background(), strings.NewReader("audio"), "sample.wav", "whisper-large-v3")
	if err == nil {
		t.Fatal("expected error")
	}
	upErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if upErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected status code: %d", upErr.StatusCode)
	}
}
