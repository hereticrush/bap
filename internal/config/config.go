/*
 * internal/config/config.go
 *
 * Loads application configuration from environment variables.
 * All settings consumed by bap flow through this struct.
 *
 * Required variables cause a fatal error if missing.
 * Optional variables fall back to sensible defaults.
 */
package config

import (
	"fmt"
	"os"
	"strconv"
)

/* Config holds the complete application configuration. */
type Config struct {
	/* AI Video Provider */
	ActiveAIProvider string /* RUNWAY, LUMA, KLING */
	RunwayAPIKey     string
	RunwayModel      string /* gen3a_turbo, gen4_turbo, gen4.5, etc. */
	RunwayMaxPerHour int    /* Sliding-window hourly rate limit for Runway API */

	/* AI Prompt Builder */
	ActivePromptBuilder string /* GEMINI, PASSTHROUGH */
	GeminiAPIKey        string
	GeminiModel         string
	GeminiMaxPerHour    int    /* Sliding-window hourly rate limit for Gemini API */

	/* Cloud Storage (S3 / Cloudflare R2) */
	S3Bucket         string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3Endpoint       string
	S3ForcePathStyle bool

	/* YouTube OAuth */
	YouTubeClientID     string
	YouTubeClientSecret string

	/* Redis (Asynq message broker) */
	RedisURL string

	/* Database */
	DBPath string

	/* Health Check Server */
	HealthPort int

	/* ElevenLabs TTS */
	ElevenLabsAPIKey  string
	ElevenLabsVoiceID string

	/* Pipeline: default when seed metadata omits use_image_anchor */
	EnableImageAnchors bool
}

/*
 * Load reads all configuration from environment variables.
 * Returns an error if any required variable is missing or invalid.
 *
 * Required variables: ACTIVE_AI_PROVIDER.
 * All other variables are optional at startup — individual subsystems
 * validate their own requirements when they initialize.
 */
func Load() (*Config, error) {
	cfg := &Config{}

	/* --- AI Video Provider --- */
	val, err := required("ACTIVE_AI_PROVIDER")
	if err != nil {
		return nil, err
	}
	cfg.ActiveAIProvider = val
	cfg.RunwayAPIKey = os.Getenv("RUNWAY_API_KEY")
	cfg.RunwayModel = withDefault("RUNWAY_MODEL", "gen3a_turbo")

	runwayRateStr := withDefault("RUNWAY_MAX_PER_HOUR", "10")
	runwayRate, err := strconv.Atoi(runwayRateStr)
	if err != nil {
		return nil, fmt.Errorf("RUNWAY_MAX_PER_HOUR must be a valid integer: %w", err)
	}
	cfg.RunwayMaxPerHour = runwayRate

	/* --- AI Prompt Builder --- */
	cfg.ActivePromptBuilder = withDefault("ACTIVE_PROMPT_BUILDER", "PASSTHROUGH")
	cfg.GeminiAPIKey = os.Getenv("GEMINI_API_KEY")
	cfg.GeminiModel = withDefault("GEMINI_MODEL", "gemini-2.5-flash")

	geminiRateStr := withDefault("GEMINI_MAX_PER_HOUR", "30")
	geminiRate, err := strconv.Atoi(geminiRateStr)
	if err != nil {
		return nil, fmt.Errorf("GEMINI_MAX_PER_HOUR must be a valid integer: %w", err)
	}
	cfg.GeminiMaxPerHour = geminiRate

	/* --- Cloud Storage --- */
	cfg.S3Bucket = os.Getenv("S3_BUCKET")
	cfg.S3Region = withDefault("S3_REGION", "auto")
	cfg.S3AccessKey = os.Getenv("S3_ACCESS_KEY")
	cfg.S3SecretKey = os.Getenv("S3_SECRET_KEY")
	cfg.S3Endpoint = os.Getenv("S3_ENDPOINT")

	s3ForcePathStyleStr := withDefault("S3_FORCE_PATH_STYLE", "false")
	s3ForcePathStyle, err := strconv.ParseBool(s3ForcePathStyleStr)
	if err != nil {
		return nil, fmt.Errorf("S3_FORCE_PATH_STYLE must be true or false: %w", err)
	}
	cfg.S3ForcePathStyle = s3ForcePathStyle

	/* --- YouTube --- */
	cfg.YouTubeClientID = os.Getenv("YOUTUBE_CLIENT_ID")
	cfg.YouTubeClientSecret = os.Getenv("YOUTUBE_CLIENT_SECRET")

	/* --- ElevenLabs --- */
	cfg.ElevenLabsAPIKey = os.Getenv("ELEVENLABS_API_KEY")
	cfg.ElevenLabsVoiceID = withDefault("ELEVENLABS_VOICE_ID", "nPczCjzI2devNBz1zQ07")

	/* --- Redis --- */
	cfg.RedisURL = withDefault("REDIS_URL", "redis://localhost:6379")

	/* --- Database --- */
	cfg.DBPath = withDefault("DB_PATH", "./data/bap.db")

	/* --- Pipeline --- */
	enableAnchorsStr := withDefault("ENABLE_IMAGE_ANCHORS", "true")
	enableAnchors, err := strconv.ParseBool(enableAnchorsStr)
	if err != nil {
		return nil, fmt.Errorf("ENABLE_IMAGE_ANCHORS must be true or false: %w", err)
	}
	cfg.EnableImageAnchors = enableAnchors

	/* --- Health Server --- */
	portStr := withDefault("HEALTH_PORT", "8081")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("HEALTH_PORT must be a valid integer: %w", err)
	}
	cfg.HealthPort = port

	return cfg, nil
}

/*
 * required reads an environment variable and returns an error
 * if it is empty or not set.
 */
func required(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return val, nil
}

/*
 * withDefault reads an environment variable, returning the
 * provided fallback value if the variable is empty or not set.
 */
func withDefault(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}
