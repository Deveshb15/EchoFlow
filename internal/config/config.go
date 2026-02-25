package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
	EnableAuth           bool
	APIBearerToken       string
	LogLevel             string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:         envOrDefault("LISTEN_ADDR", ":8080"),
		UpstreamBaseURL:    strings.TrimRight(envOrDefault("UPSTREAM_BASE_URL", "https://api.groq.com/openai/v1"), "/"),
		UpstreamAPIKey:     strings.TrimSpace(os.Getenv("UPSTREAM_API_KEY")),
		TranscriptionModel: envOrDefault("TRANSCRIPTION_MODEL", "whisper-large-v3"),
		PostProcessModel:   envOrDefault("POSTPROCESS_MODEL", "meta-llama/llama-4-scout-17b-16e-instruct"),
		LogLevel:           strings.ToLower(envOrDefault("LOG_LEVEL", "info")),
	}

	var err error
	if cfg.RequestTimeout, err = secondsEnv("REQUEST_TIMEOUT_SECONDS", 25); err != nil {
		return Config{}, err
	}
	if cfg.TranscriptionTimeout, err = secondsEnv("TRANSCRIPTION_TIMEOUT_SECONDS", 20); err != nil {
		return Config{}, err
	}
	if cfg.PostProcessTimeout, err = secondsEnv("POSTPROCESS_TIMEOUT_SECONDS", 20); err != nil {
		return Config{}, err
	}
	if cfg.MaxUploadBytes, err = int64Env("MAX_UPLOAD_BYTES", 25*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.EnableAuth, err = boolEnv("ENABLE_AUTH", false); err != nil {
		return Config{}, err
	}
	cfg.APIBearerToken = strings.TrimSpace(os.Getenv("API_BEARER_TOKEN"))

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
	if c.UpstreamAPIKey == "" {
		return errors.New("UPSTREAM_API_KEY is required")
	}
	if c.TranscriptionModel == "" {
		return errors.New("TRANSCRIPTION_MODEL must not be empty")
	}
	if c.PostProcessModel == "" {
		return errors.New("POSTPROCESS_MODEL must not be empty")
	}
	if c.MaxUploadBytes <= 0 {
		return errors.New("MAX_UPLOAD_BYTES must be > 0")
	}
	if c.EnableAuth && c.APIBearerToken == "" {
		return errors.New("API_BEARER_TOKEN is required when ENABLE_AUTH=true")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func secondsEnv(key string, fallback int) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return time.Duration(fallback) * time.Second, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be > 0", key)
	}
	return time.Duration(seconds) * time.Second, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be > 0", key)
	}
	return n, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a bool: %w", key, err)
	}
	return b, nil
}
