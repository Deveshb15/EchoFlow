package config

import (
	"errors"
	"strings"
	"time"

	cenv "github.com/caarlos0/env/v11"
)

type Config struct {
	ListenAddr           string
	UpstreamBaseURL      string
	UpstreamAPIKey       string
	TranscriptionModel   string
	PostProcessModel     string
	RequestTimeout       time.Duration
	TranscriptionTimeout time.Duration
	PostProcessTimeout   time.Duration
	MaxUploadBytes       int64
	LogLevel             string
}

type envConfig struct {
	ListenAddr                  string `env:"LISTEN_ADDR" envDefault:":8080"`
	UpstreamBaseURL             string `env:"UPSTREAM_BASE_URL" envDefault:"https://api.groq.com/openai/v1"`
	UpstreamAPIKey              string `env:"UPSTREAM_API_KEY"`
	TranscriptionModel          string `env:"TRANSCRIPTION_MODEL" envDefault:"whisper-large-v3"`
	PostProcessModel            string `env:"POSTPROCESS_MODEL" envDefault:"meta-llama/llama-4-scout-17b-16e-instruct"`
	RequestTimeoutSeconds       int    `env:"REQUEST_TIMEOUT_SECONDS" envDefault:"25"`
	TranscriptionTimeoutSeconds int    `env:"TRANSCRIPTION_TIMEOUT_SECONDS" envDefault:"20"`
	PostProcessTimeoutSeconds   int    `env:"POSTPROCESS_TIMEOUT_SECONDS" envDefault:"20"`
	MaxUploadBytes              int64  `env:"MAX_UPLOAD_BYTES" envDefault:"26214400"`
	LogLevel                    string `env:"LOG_LEVEL" envDefault:"info"`
}

func Load() (Config, error) {
	var raw envConfig
	if err := cenv.Parse(&raw); err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:           strings.TrimSpace(raw.ListenAddr),
		UpstreamBaseURL:      strings.TrimRight(strings.TrimSpace(raw.UpstreamBaseURL), "/"),
		UpstreamAPIKey:       strings.TrimSpace(raw.UpstreamAPIKey),
		TranscriptionModel:   strings.TrimSpace(raw.TranscriptionModel),
		PostProcessModel:     strings.TrimSpace(raw.PostProcessModel),
		RequestTimeout:       time.Duration(raw.RequestTimeoutSeconds) * time.Second,
		TranscriptionTimeout: time.Duration(raw.TranscriptionTimeoutSeconds) * time.Second,
		PostProcessTimeout:   time.Duration(raw.PostProcessTimeoutSeconds) * time.Second,
		MaxUploadBytes:       raw.MaxUploadBytes,
		LogLevel:             strings.ToLower(strings.TrimSpace(raw.LogLevel)),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("LISTEN_ADDR must not be empty")
	}
	if c.UpstreamBaseURL == "" {
		return errors.New("UPSTREAM_BASE_URL must not be empty")
	}
	if c.TranscriptionModel == "" {
		return errors.New("TRANSCRIPTION_MODEL must not be empty")
	}
	if c.PostProcessModel == "" {
		return errors.New("POSTPROCESS_MODEL must not be empty")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("REQUEST_TIMEOUT_SECONDS must be > 0")
	}
	if c.TranscriptionTimeout <= 0 {
		return errors.New("TRANSCRIPTION_TIMEOUT_SECONDS must be > 0")
	}
	if c.PostProcessTimeout <= 0 {
		return errors.New("POSTPROCESS_TIMEOUT_SECONDS must be > 0")
	}
	if c.MaxUploadBytes <= 0 {
		return errors.New("MAX_UPLOAD_BYTES must be > 0")
	}
	return nil
}
