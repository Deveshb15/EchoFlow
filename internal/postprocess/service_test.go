package postprocess

import (
	"context"
	"strings"
	"testing"
	"time"

	"echoflow/internal/upstream/openai"
)

type fakeChatClient struct {
	request openai.ChatCompletionRequest
	resp    openai.ChatCompletionResponse
	err     error
}

func (f *fakeChatClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	f.request = req
	return f.resp, f.err
}

func TestMergedVocabularyTermsDedupesCaseInsensitively(t *testing.T) {
	terms := mergedVocabularyTerms("Alice, bob\nALICE; Bob; Carol")
	got := strings.Join(terms, ",")
	want := "Alice,bob,Carol"
	if got != want {
		t.Fatalf("unexpected terms: got %q want %q", got, want)
	}
}

func TestSanitizePostProcessedTranscript(t *testing.T) {
	cases := map[string]string{
		"\"Hello world\"": "Hello world",
		"EMPTY":           "",
		"  hi  ":          "hi",
		"":                "",
	}

	for in, want := range cases {
		if got := sanitizePostProcessedTranscript(in); got != want {
			t.Fatalf("sanitize(%q): got %q want %q", in, got, want)
		}
	}
}

func TestProcessBuildsPromptAndReturnsUsage(t *testing.T) {
	client := &fakeChatClient{resp: openai.ChatCompletionResponse{
		Content: "\"Hello Alice\"",
		Usage: &openai.TokenUsage{
			PromptTokens:     111,
			CompletionTokens: 12,
			TotalTokens:      123,
		},
	}}
	svc := New(client, "test-model", 2*time.Second)

	result, err := svc.Process(context.Background(), Input{
		Transcript:         "hello alise",
		ContextSummary:     "Replying to Alice in email.",
		CustomVocabulary:   "Alice, Project X",
		CustomSystemPrompt: "",
		IncludeDebugPrompt: true,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if result.Transcript != "Hello Alice" {
		t.Fatalf("unexpected transcript: %q", result.Transcript)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 123 {
		t.Fatalf("expected usage to be returned, got %+v", result.Usage)
	}
	if client.request.Model != "test-model" {
		t.Fatalf("unexpected model: %q", client.request.Model)
	}
	if len(client.request.Messages) != 2 {
		t.Fatalf("unexpected message count: %d", len(client.request.Messages))
	}
	systemContent, _ := client.request.Messages[0].Content.(string)
	if !strings.Contains(systemContent, "Project X") {
		t.Fatalf("expected vocabulary in system prompt, got %q", systemContent)
	}
}
