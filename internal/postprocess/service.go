package postprocess

import (
	"context"
	"fmt"
	"strings"
	"time"

	"echoflow/internal/upstream/openai"
)

const DefaultSystemPrompt = `You are a dictation post-processor. You receive raw speech-to-text output and return clean text ready to be typed into an application.

Your job:
- Remove filler words (um, uh, you know, like) unless they carry meaning.
- Fix spelling, grammar, and punctuation errors.
- When the transcript already contains a word that is a close misspelling of a name or term from the context or custom vocabulary, correct the spelling. Never insert names or terms from context that the speaker did not say.
- Preserve the speaker's intent, tone, and meaning exactly.

Output rules:
- Return ONLY the cleaned transcript text, nothing else.
- If the transcription is empty, return exactly: EMPTY
- Do not add words, names, or content that are not in the transcription. The context is only for correcting spelling of words already spoken.
- Do not change the meaning of what was said.`

const DefaultSystemPromptDate = "2026-02-24"

type ChatClient interface {
	ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Input struct {
	Transcript         string
	ContextSummary     string
	CustomVocabulary   string
	CustomSystemPrompt string
	Model              string
	// Deprecated: accepted for compatibility; prompts are no longer returned in API responses.
	IncludeDebugPrompt bool
}

type Result struct {
	Transcript string
	Usage      *TokenUsage
}

type Service struct {
	client       ChatClient
	defaultModel string
	timeout      time.Duration
}

func New(client ChatClient, defaultModel string, timeout time.Duration) *Service {
	return &Service{
		client:       client,
		defaultModel: strings.TrimSpace(defaultModel),
		timeout:      timeout,
	}
}

func (s *Service) Process(ctx context.Context, in Input) (Result, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = s.defaultModel
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	vocabularyTerms := mergedVocabularyTerms(in.CustomVocabulary)
	normalizedVocabulary := normalizedVocabularyText(vocabularyTerms)

	vocabularyPrompt := ""
	if normalizedVocabulary != "" {
		vocabularyPrompt = fmt.Sprintf(`The following vocabulary must be treated as high-priority terms while rewriting.
Use these spellings exactly in the output when relevant:
%s`, normalizedVocabulary)
	}

	systemPrompt := strings.TrimSpace(in.CustomSystemPrompt)
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}
	if vocabularyPrompt != "" {
		systemPrompt += "\n\n" + vocabularyPrompt
	}

	userMessage := fmt.Sprintf(`Instructions: Clean up RAW_TRANSCRIPTION and return only the cleaned transcript text without surrounding quotes. Return EMPTY if there should be no result.

CONTEXT: %q

RAW_TRANSCRIPTION: %q`, in.ContextSummary, in.Transcript)

	chatResp, err := s.client.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:       model,
		Temperature: 0.0,
		Messages: []openai.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
	})
	if err != nil {
		return Result{}, err
	}

	result := Result{Transcript: sanitizePostProcessedTranscript(chatResp.Content)}
	if chatResp.Usage != nil {
		result.Usage = &TokenUsage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		}
	}
	return result, nil
}

func sanitizePostProcessedTranscript(value string) string {
	result := strings.TrimSpace(value)
	if result == "" {
		return ""
	}
	if strings.HasPrefix(result, "\"") && strings.HasSuffix(result, "\"") && len(result) > 1 {
		result = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(result, "\""), "\""))
	}
	if result == "EMPTY" {
		return ""
	}
	return result
}

func mergedVocabularyTerms(rawVocabulary string) []string {
	fields := strings.FieldsFunc(rawVocabulary, func(r rune) bool {
		return r == '\n' || r == ',' || r == ';'
	})

	seen := make(map[string]struct{}, len(fields))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		term := strings.TrimSpace(field)
		if term == "" {
			continue
		}
		key := strings.ToLower(term)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func normalizedVocabularyText(vocabularyTerms []string) string {
	terms := make([]string, 0, len(vocabularyTerms))
	for _, term := range vocabularyTerms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			continue
		}
		terms = append(terms, trimmed)
	}
	return strings.Join(terms, ", ")
}
