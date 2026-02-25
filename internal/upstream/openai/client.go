package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type ObserverFunc func(endpoint string, status int, duration time.Duration)

type Option func(*Client)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	observer   ObserverFunc
}

type Error struct {
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("upstream request failed with status %d", e.StatusCode)
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Messages    []ChatMessage `json:"messages"`
}

type ChatCompletionResponse struct {
	Content string
	Usage   *TokenUsage
}

func WithObserver(observer ObserverFunc) Option {
	return func(c *Client) {
		c.observer = observer
	}
}

func New(baseURL, apiKey string, httpClient *http.Client, opts ...Option) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpClient,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *Client) Transcribe(ctx context.Context, file io.Reader, fileName, model string) (string, error) {
	started := time.Now()
	statusCode := 0
	defer c.observe("audio_transcriptions", statusCode, time.Since(started))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("model", model); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	url := c.baseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body.Bytes()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", &Error{StatusCode: resp.StatusCode, Body: truncateBody(string(respBody))}
	}

	return parseTranscript(respBody)
}

func (c *Client) ChatCompletion(ctx context.Context, reqPayload ChatCompletionRequest) (ChatCompletionResponse, error) {
	started := time.Now()
	statusCode := 0
	defer c.observe("chat_completions", statusCode, time.Since(started))

	payload, err := json.Marshal(reqPayload)
	if err != nil {
		return ChatCompletionResponse{}, err
	}

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return ChatCompletionResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatCompletionResponse{}, err
	}

	if resp.StatusCode != http.StatusOK {
		return ChatCompletionResponse{}, &Error{StatusCode: resp.StatusCode, Body: truncateBody(string(respBody))}
	}

	return parseChatCompletion(respBody)
}

func (c *Client) CheckModels(ctx context.Context) error {
	started := time.Now()
	statusCode := 0
	defer c.observe("models", statusCode, time.Since(started))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &Error{StatusCode: resp.StatusCode, Body: truncateBody(string(body))}
	}
	return nil
}

func (c *Client) observe(endpoint string, status int, duration time.Duration) {
	if c.observer != nil {
		c.observer(endpoint, status, duration)
	}
}

func parseTranscript(data []byte) (string, error) {
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil && parsed.Text != "" {
		return parsed.Text, nil
	}

	plainText := strings.TrimSpace(joinLines(string(data)))
	if plainText == "" {
		return "", fmt.Errorf("invalid transcription response")
	}
	return plainText, nil
}

func parseChatCompletion(data []byte) (ChatCompletionResponse, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("invalid chat completion response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return ChatCompletionResponse{}, fmt.Errorf("missing choices")
	}
	content := parsed.Choices[0].Message.Content
	if content == "" {
		return ChatCompletionResponse{}, fmt.Errorf("missing choices[0].message.content")
	}

	resp := ChatCompletionResponse{Content: content}
	if parsed.Usage != nil {
		resp.Usage = &TokenUsage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		}
	}
	return resp, nil
}

func joinLines(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	return strings.Join(parts, " ")
}

func truncateBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4096 {
		return s
	}
	return s[:4096] + "..."
}
