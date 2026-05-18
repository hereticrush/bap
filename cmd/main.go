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
	"path/filepath"
	"syscall"
	"time"

	"github.com/hereticrush/bap/internal/adapter/image"
	"github.com/hereticrush/bap/internal/adapter/prompt"
	"github.com/hereticrush/bap/internal/adapter/tts"
	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/adapter/youtube"
	"github.com/hereticrush/bap/internal/batch"
	"github.com/hereticrush/bap/internal/config"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hereticrush/bap/internal/health"
	"github.com/hereticrush/bap/internal/worker"
)

/*
 * Version information injected at build time via ldflags.
 * Populated by: make build
 *
 * Example:
 *   go build -ldflags "-X main.version=v1.0.0 -X main.commit=abc1234 -X main.date=2026-01-01"
 */
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
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
	case "auth-youtube":
		runAuthYoutube()
	case "version":
		runVersion()
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

	/* Select the AI video provider */
	videoProvider, err := selectVideoProvider(cfg)
	if err != nil {
		slog.Error("failed to select video provider", "error", err)
		os.Exit(1)
	}

	/* Initialize the YouTube publisher */
	youtubePublisher := youtube.NewPublisher(
		filepath.Join("credentials", "youtube", "client_secret.json"),
		filepath.Join("credentials", "youtube", "token.json"),
	)

	/* Initialize the ElevenLabs TTS provider */
	elevenlabsKey := os.Getenv("ELEVENLABS_API_KEY")
	ttsProvider := tts.NewElevenLabsAdapter(elevenlabsKey, "nPczCjzI2devNBz1zQ07") // default voice Brian

	/* Initialize Image provider */
	imageProvider := image.NewPollinationsAdapter()

	/* Initialize Asynq scheduler and workers */
	go func() {
		if err := worker.RunServer(cfg.RedisURL, database, videoProvider, youtubePublisher, ttsProvider, imageProvider, filepath.Join("data", "videos")); err != nil {
			slog.Error("asynq worker server failed", "error", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := worker.RunScheduler(cfg.RedisURL); err != nil {
			slog.Error("asynq scheduler failed", "error", err)
			os.Exit(1)
		}
	}()

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
	case "GEMINI":
		return prompt.NewGeminiAdapter(cfg.GeminiAPIKey, cfg.GeminiModel, cfg.GeminiMaxPerHour), nil
	default:
		return nil, fmt.Errorf("unknown prompt builder: %s", cfg.ActivePromptBuilder)
	}
}

/*
 * selectVideoProvider returns the AIVideoProvider adapter matching
 * the ACTIVE_AI_PROVIDER config value.
 */
func selectVideoProvider(cfg *config.Config) (video.AIVideoProvider, error) {
	switch cfg.ActiveAIProvider {
	case "RUNWAY":
		return video.NewRunwayAdapter(cfg.RunwayAPIKey, cfg.RunwayModel, cfg.RunwayMaxPerHour), nil
	default:
		return nil, fmt.Errorf("unknown video provider: %s", cfg.ActiveAIProvider)
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
	fmt.Fprintln(os.Stderr, "  auth-youtube     Interactive OAuth web flow to generate YouTube token.json")
	fmt.Fprintln(os.Stderr, "  version          Print build version information")
}

/*
 * runAuthYoutube initiates the OAuth2 web flow for YouTube.
 * It expects credentials/youtube/client_secret.json to exist.
 */
func runAuthYoutube() {
	clientSecretPath := filepath.Join("credentials", "youtube", "client_secret.json")
	tokenPath := filepath.Join("credentials", "youtube", "token.json")

	// Ensure the directories exist
	if err := os.MkdirAll(filepath.Dir(clientSecretPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create directory: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(clientSecretPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: %s does not exist.\n", clientSecretPath)
		fmt.Fprintln(os.Stderr, "Please download the OAuth 2.0 Client ID (Desktop app) from Google Cloud Console and place it there.")
		os.Exit(1)
	}

	fmt.Println("Starting YouTube OAuth2 web flow...")
	if err := youtube.RunAuthFlow(clientSecretPath, tokenPath); err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nAuthentication successful! token.json has been saved.")
	fmt.Println("The 'serve' command can now publish videos to YouTube.")
}

/*
 * runVersion prints the build version information injected
 * via ldflags at compile time.
 *
 * Usage: bap version
 */
func runVersion() {
	fmt.Printf("bap %s (commit: %s, built: %s)\n", version, commit, date)
}
