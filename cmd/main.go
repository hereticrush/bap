/*
 * cmd/main.go
 *
 * Entry point for the bap (Build And Post) application.
 * Dispatches CLI subcommands:
 *   - serve          Start the pipeline scheduler, workers, and health server.
 *   - build-prompts  Atomically enrich seed prompts via LLM and load into database.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hereticrush/bap/internal/adapter/prompt"
	"github.com/hereticrush/bap/internal/batch"
	"github.com/hereticrush/bap/internal/config"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hereticrush/bap/internal/health"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		runServe()
	case "build-prompts":
		runBuildPrompts()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

/*
 * runServe starts the Asynq scheduler, workers, and the /healthz
 * HTTP server. This is the main long-running process for the VPS.
 */
func runServe() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	slog.Info("bap started",
		"command", "serve",
		"ai_provider", cfg.ActiveAIProvider,
		"db_path", cfg.DBPath,
		"health_port", cfg.HealthPort,
	)

	/* Start the /healthz HTTP server in a background goroutine */
	healthSrv := health.New(cfg.HealthPort, database)
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server failed", "error", err)
			os.Exit(1)
		}
	}()

	/* TODO: Initialize Asynq scheduler and workers here */

	/* Block until SIGINT or SIGTERM */
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	slog.Info("shutdown signal received, draining...")

	/* Graceful shutdown with a 5-second deadline */
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("health server shutdown error", "error", err)
	}

	slog.Info("bap stopped gracefully")
}

/*
 * runBuildPrompts executes the atomic batch prompt enrichment.
 * Reads a JSON seed file, enriches each seed via the configured
 * AIPromptBuilder, and commits all results to SQLite atomically.
 *
 * Usage: bap build-prompts ./weekly_seeds.json
 */
func runBuildPrompts() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: bap build-prompts <seeds.json>")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	seedFile := os.Args[2]

	/* Parse the JSON seed file */
	batchInput, err := parseSeedFile(seedFile)
	if err != nil {
		slog.Error("failed to parse seed file", "file", seedFile, "error", err)
		os.Exit(1)
	}

	slog.Info("seed file parsed",
		"file", seedFile,
		"seeds", len(batchInput.Seeds),
		"target_provider", batchInput.TargetProvider,
	)

	/* Select the prompt builder adapter */
	builder, err := selectBuilder(cfg)
	if err != nil {
		slog.Error("failed to select prompt builder", "error", err)
		os.Exit(1)
	}

	/* Execute the atomic batch build */
	ctx := context.Background()
	if err := batch.BuildBatch(ctx, database, builder, *batchInput); err != nil {
		slog.Error("batch build failed", "error", err)
		os.Exit(1)
	}

	slog.Info("build-prompts completed successfully")
}

/*
 * parseSeedFile reads and validates a JSON seed file into a BatchInput.
 * Returns an error if the file cannot be read, parsed, or is empty.
 */
func parseSeedFile(path string) (*prompt.BatchInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var input prompt.BatchInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	if len(input.Seeds) == 0 {
		return nil, fmt.Errorf("seed file contains no seeds")
	}

	if input.TargetProvider == "" {
		return nil, fmt.Errorf("target_provider is required")
	}

	return &input, nil
}

/*
 * selectBuilder returns the AIPromptBuilder adapter matching
 * the ACTIVE_PROMPT_BUILDER config value.
 */
func selectBuilder(cfg *config.Config) (prompt.AIPromptBuilder, error) {
	switch cfg.ActivePromptBuilder {
	case "PASSTHROUGH":
		return prompt.NewPassthroughAdapter(), nil
	/* TODO: case "GEMINI": return prompt.NewGeminiAdapter(cfg.GeminiAPIKey, cfg.GeminiModel), nil */
	default:
		return nil, fmt.Errorf("unknown prompt builder: %s", cfg.ActivePromptBuilder)
	}
}

/*
 * printUsage outputs the available subcommands to stderr.
 */
func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: bap <command> [arguments]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  serve            Start the pipeline scheduler, workers, and health server")
	fmt.Fprintln(os.Stderr, "  build-prompts    Atomically enrich seed prompts via LLM (requires <seeds.json>)")
}
