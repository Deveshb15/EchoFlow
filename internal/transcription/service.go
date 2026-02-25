package transcription

import (
	"context"
	"io"
	"strings"
	"time"
)

type Client interface {
	Transcribe(ctx context.Context, file io.Reader, fileName, model string) (string, error)
}

type Service struct {
	client       Client
	defaultModel string
	timeout      time.Duration
}

func New(client Client, defaultModel string, timeout time.Duration) *Service {
	return &Service{
		client:       client,
		defaultModel: strings.TrimSpace(defaultModel),
		timeout:      timeout,
	}
}

func (s *Service) Transcribe(ctx context.Context, file io.Reader, fileName, model string) (string, error) {
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		selectedModel = s.defaultModel
	}
	if fileName == "" {
		fileName = "audio.wav"
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	text, err := s.client.Transcribe(ctx, file, fileName, selectedModel)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}
