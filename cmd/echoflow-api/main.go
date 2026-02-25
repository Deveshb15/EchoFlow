package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"echoflow/internal/config"
	"echoflow/internal/httpapi"
	"echoflow/internal/pipeline"
	"echoflow/internal/postprocess"
	"echoflow/internal/transcription"
	"echoflow/internal/upstream/openai"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)

	upstreamHTTPClient := &http.Client{Timeout: cfg.RequestTimeout}
	upstreamClient := openai.New(cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, upstreamHTTPClient)

	transcriptionService := transcription.New(upstreamClient, cfg.TranscriptionModel, cfg.TranscriptionTimeout)
	postProcessService := postprocess.New(upstreamClient, cfg.PostProcessModel, cfg.PostProcessTimeout)
	pipelineService := pipeline.New(transcriptionService, postProcessService, cfg.TranscriptionModel, cfg.PostProcessModel)

	handler := httpapi.NewServer(cfg, logger, httpapi.Dependencies{
		Transcription: transcriptionService,
		PostProcess:   postProcessService,
		Pipeline:      pipelineService,
		Upstream:      upstreamClient,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("server exited", "error", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}
