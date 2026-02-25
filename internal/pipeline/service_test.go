package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"echoflow/internal/postprocess"
)

type fakeTranscriber struct {
	text string
	err  error
}

func (f *fakeTranscriber) Transcribe(_ context.Context, file io.Reader, _ string, _ string) (string, error) {
	_, _ = io.ReadAll(file)
	return f.text, f.err
}

type fakePostProcessor struct {
	result postprocess.Result
	err    error
	input  postprocess.Input
}

func (f *fakePostProcessor) Process(_ context.Context, in postprocess.Input) (postprocess.Result, error) {
	f.input = in
	return f.result, f.err
}

func TestProcessFallsBackToRawTranscriptOnPostProcessError(t *testing.T) {
	svc := New(
		&fakeTranscriber{text: "  raw transcript  "},
		&fakePostProcessor{err: errors.New("boom")},
		"whisper-large-v3",
		"llama",
	)

	res, err := svc.Process(context.Background(), ProcessInput{
		File:           strings.NewReader("audio"),
		FileName:       "test.wav",
		ContextSummary: "test context",
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if res.RawTranscript != "raw transcript" {
		t.Fatalf("unexpected raw transcript: %q", res.RawTranscript)
	}
	if res.FinalTranscript != "raw transcript" {
		t.Fatalf("expected fallback final transcript, got %q", res.FinalTranscript)
	}
	if res.PostProcessingStatus != "Post-processing failed, using raw transcript" {
		t.Fatalf("unexpected status: %q", res.PostProcessingStatus)
	}
	if res.PostProcessingUsage != nil {
		t.Fatalf("expected no usage on fallback, got %+v", res.PostProcessingUsage)
	}
}

func TestProcessSuccessReturnsUsage(t *testing.T) {
	pp := &fakePostProcessor{result: postprocess.Result{
		Transcript: "clean",
		Usage: &postprocess.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 20,
			TotalTokens:      120,
		},
	}}
	svc := New(&fakeTranscriber{text: "raw"}, pp, "whisper", "llama")

	res, err := svc.Process(context.Background(), ProcessInput{
		File:         strings.NewReader("audio"),
		FileName:     "test.wav",
		IncludeDebug: true,
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if res.FinalTranscript != "clean" {
		t.Fatalf("unexpected final transcript: %q", res.FinalTranscript)
	}
	if res.PostProcessingUsage == nil || res.PostProcessingUsage.TotalTokens != 120 {
		t.Fatalf("unexpected usage: %+v", res.PostProcessingUsage)
	}
	if !pp.input.IncludeDebugPrompt {
		t.Fatal("expected IncludeDebugPrompt to still be forwarded for compatibility")
	}
}
