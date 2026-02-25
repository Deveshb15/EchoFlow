package pipeline

import (
	"context"
	"io"
	"strings"
	"time"

	"echoflow/internal/postprocess"
)

type Transcriber interface {
	Transcribe(ctx context.Context, file io.Reader, fileName, model string) (string, error)
}

type PostProcessor interface {
	Process(ctx context.Context, in postprocess.Input) (postprocess.Result, error)
}

type Service struct {
	transcriber               Transcriber
	postProcessor             PostProcessor
	defaultTranscriptionModel string
	defaultPostProcessModel   string
}

type ProcessInput struct {
	File               io.Reader
	FileName           string
	ContextSummary     string
	CustomVocabulary   string
	CustomSystemPrompt string
	TranscriptionModel string
	PostProcessModel   string
	// Deprecated: parsed for backward compatibility; debug prompts are no longer returned.
	IncludeDebug bool
}

type Timings struct {
	Transcription  time.Duration
	PostProcessing time.Duration
	Total          time.Duration
}

type ProcessResult struct {
	RawTranscript        string
	FinalTranscript      string
	PostProcessingStatus string
	PostProcessingUsage  *postprocess.TokenUsage
	Timings              Timings
}

func New(transcriber Transcriber, postProcessor PostProcessor, defaultTranscriptionModel, defaultPostProcessModel string) *Service {
	return &Service{
		transcriber:               transcriber,
		postProcessor:             postProcessor,
		defaultTranscriptionModel: strings.TrimSpace(defaultTranscriptionModel),
		defaultPostProcessModel:   strings.TrimSpace(defaultPostProcessModel),
	}
}

func (s *Service) Process(ctx context.Context, in ProcessInput) (ProcessResult, error) {
	started := time.Now()
	transcriptionStarted := time.Now()

	transcriptionModel := strings.TrimSpace(in.TranscriptionModel)
	if transcriptionModel == "" {
		transcriptionModel = s.defaultTranscriptionModel
	}
	postProcessModel := strings.TrimSpace(in.PostProcessModel)
	if postProcessModel == "" {
		postProcessModel = s.defaultPostProcessModel
	}

	rawTranscript, err := s.transcriber.Transcribe(ctx, in.File, in.FileName, transcriptionModel)
	transcriptionDuration := time.Since(transcriptionStarted)
	if err != nil {
		return ProcessResult{}, err
	}
	rawTranscript = strings.TrimSpace(rawTranscript)

	postProcessingStarted := time.Now()
	postResult, postErr := s.postProcessor.Process(ctx, postprocess.Input{
		Transcript:         rawTranscript,
		ContextSummary:     strings.TrimSpace(in.ContextSummary),
		CustomVocabulary:   in.CustomVocabulary,
		CustomSystemPrompt: in.CustomSystemPrompt,
		Model:              postProcessModel,
		IncludeDebugPrompt: in.IncludeDebug,
	})
	postProcessingDuration := time.Since(postProcessingStarted)

	result := ProcessResult{
		RawTranscript: rawTranscript,
		Timings: Timings{
			Transcription:  transcriptionDuration,
			PostProcessing: postProcessingDuration,
			Total:          time.Since(started),
		},
	}

	if postErr != nil {
		result.FinalTranscript = rawTranscript
		result.PostProcessingStatus = "Post-processing failed, using raw transcript"
		return result, nil
	}

	result.FinalTranscript = strings.TrimSpace(postResult.Transcript)
	result.PostProcessingStatus = "Post-processing succeeded"
	result.PostProcessingUsage = postResult.Usage
	result.Timings.Total = time.Since(started)
	return result, nil
}
