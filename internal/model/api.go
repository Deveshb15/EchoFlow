package model

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ErrorResponse struct {
	Error     APIError `json:"error"`
	RequestID string   `json:"request_id,omitempty"`
}

type HealthResponse struct {
	OK bool `json:"ok"`
}

type ReadyResponse struct {
	OK          bool   `json:"ok"`
	ServiceName string `json:"service_name,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type TranscriptionResponse struct {
	Text string `json:"text"`
}

type PostProcessRequest struct {
	Transcript         string `json:"transcript"`
	ContextSummary     string `json:"context_summary"`
	CustomVocabulary   string `json:"custom_vocabulary,omitempty"`
	CustomSystemPrompt string `json:"custom_system_prompt,omitempty"`
	Model              string `json:"model,omitempty"`
	// Deprecated: accepted for backwards compatibility, ignored in responses.
	IncludeDebugPrompt bool `json:"include_debug_prompt,omitempty"`
}

type PostProcessResponse struct {
	Transcript string      `json:"transcript"`
	Status     string      `json:"status"`
	Usage      *TokenUsage `json:"usage,omitempty"`
}

type PipelineTimings struct {
	Transcription  int64 `json:"transcription"`
	PostProcessing int64 `json:"post_processing"`
	Total          int64 `json:"total"`
}

type PipelineProcessResponse struct {
	RawTranscript        string          `json:"raw_transcript"`
	FinalTranscript      string          `json:"final_transcript"`
	PostProcessingStatus string          `json:"post_processing_status"`
	PostProcessingUsage  *TokenUsage     `json:"post_processing_usage,omitempty"`
	TimingsMS            PipelineTimings `json:"timings_ms"`
}
