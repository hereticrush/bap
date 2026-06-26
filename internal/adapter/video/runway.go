/*
 * internal/adapter/video/runway.go
 *
 * RunwayAdapter implements AIVideoProvider by calling the
 * Runway REST API (api.dev.runwayml.com).
 *
 * Uses stdlib net/http only — no SDK dependency.
 *
 * Safety features:
 *   - Sliding-window hourly rate limiter (configurable via maxPerHour)
 *   - Full API error JSON logged and propagated on any non-200 response
 *   - Default fallbacks for duration (5s) and ratio (1280:720)
 *
 * API Reference:
 *   https://docs.dev.runwayml.com/guides/using-the-api/
 *   https://docs.dev.runwayml.com/api
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hereticrush/bap/internal/adapter/storage"
)

/* Runway REST API base URL and required version header. */
var runwayBaseURL = "https://api.dev.runwayml.com/v1"

const runwayAPIVersion = "2024-11-06"

/* RunwayAdapter generates videos via the Runway REST API. */
type RunwayAdapter struct {
	apiKey     string
	model      string
	maxPerHour int
	callLog    []time.Time
	mu         sync.Mutex
	client     *http.Client
	storage    storage.StorageProvider
}

/*
 * NewRunwayAdapter creates a RunwayAdapter with the given API key,
 * model name, hourly rate limit cap, and cloud storage provider.
 *
 * Parameters:
 *   apiKey     — Runway API secret for Bearer authentication
 *   model      — Model identifier (e.g., "gen3a_turbo", "gen4_turbo")
 *   maxPerHour — Maximum API calls allowed per rolling hour
 *   storage    — Storage provider for potential fallback uploads
 */
func NewRunwayAdapter(apiKey, model string, maxPerHour int, storage storage.StorageProvider) *RunwayAdapter {
	return &RunwayAdapter{
		apiKey:     apiKey,
		model:      model,
		maxPerHour: maxPerHour,
		callLog:    make([]time.Time, 0, maxPerHour),
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		storage:    storage,
	}
}

/* Name returns the provider identifier. */
func (r *RunwayAdapter) Name() string {
	return "RUNWAY"
}

/*
 * GenerateVideo submits a text-to-video generation request to Runway.
 * Enforces the hourly rate limit before each call and logs full API
 * error JSON on failure.
 *
 * Flow:
 *   1. Check hourly rate limit (sliding window)
 *   2. Build the image_to_video JSON payload (text-only mode)
 *   3. POST to /v1/image_to_video
 *   4. Parse response — extract task ID
 *   5. Return task ID or wrapped error
 *
 * Default fallbacks:
 *   - Duration: 5 seconds when req.Duration == 0
 *   - Ratio: "1280:720" when req.AspectRatio is empty
 */
func (r *RunwayAdapter) GenerateVideo(ctx context.Context, req GenerationRequest) (string, error) {
	/* Step 1: Enforce hourly rate limit */
	if err := r.checkRateLimit(); err != nil {
		return "", err
	}

	/* Step 2: Build the request payload with defaults */
	duration := req.Duration
	if duration == 0 {
		duration = 5
	}

	ratio := req.AspectRatio
	if ratio == "9:16" {
		ratio = "720:1280"
	} else if ratio == "16:9" {
		ratio = "1280:720"
	}
	if ratio == "" {
		ratio = "1280:720"
	}

	payload := runwayCreateRequest{
		Model:      r.model,
		PromptText: req.Prompt,
		Ratio:      ratio,
		Duration:   duration,
	}

	if len(req.ImageURLs) > 0 {
		payload.PromptImage = req.ImageURLs[0]
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	/* Step 3: POST to Runway API */
	url := fmt.Sprintf("%s/image_to_video", runwayBaseURL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	r.SetHeaders(httpReq)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("runway HTTP call: %w", err)
	}
	defer resp.Body.Close()

	/* Read the full response body */
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	/* Non-200: log full JSON error and return wrapped error */
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("runway API error",
			"status_code", resp.StatusCode,
			"model", r.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf(
			"runway API returned %d: %s", resp.StatusCode, string(respBody),
		)
	}

	/* Step 4: Parse the task ID from the response */
	var createResp runwayCreateResponse
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return "", fmt.Errorf("parse runway response: %w", err)
	}

	if createResp.ID == "" {
		slog.Error("runway returned empty task ID",
			"model", r.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("runway returned empty task ID")
	}

	slog.Info("runway video generation submitted",
		"task_id", createResp.ID,
		"model", r.model,
		"duration", duration,
		"ratio", ratio,
	)

	return createResp.ID, nil
}

/*
 * CheckStatus polls Runway for the current state of a generation
 * task identified by taskID.
 *
 * Flow:
 *   1. GET /v1/tasks/{taskID}
 *   2. Parse response — map Runway status to GenerationStatus
 *   3. Return GenerationResult with VideoURL or error detail
 */
func (r *RunwayAdapter) CheckStatus(ctx context.Context, taskID string) (GenerationResult, error) {
	url := fmt.Sprintf("%s/tasks/%s", runwayBaseURL, taskID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("create request: %w", err)
	}
	r.SetHeaders(httpReq)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("runway HTTP call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("runway task status error",
			"status_code", resp.StatusCode,
			"task_id", taskID,
			"response_body", string(respBody),
		)
		return GenerationResult{}, fmt.Errorf(
			"runway task status returned %d: %s", resp.StatusCode, string(respBody),
		)
	}

	var taskResp runwayTaskResponse
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		return GenerationResult{}, fmt.Errorf("parse task response: %w", err)
	}

	return taskResp.toGenerationResult(), nil
}

/*
 * checkRateLimit enforces the sliding-window hourly rate limit.
 * Thread-safe via mutex — safe for concurrent worker access.
 *
 * Algorithm:
 *   1. Lock mutex
 *   2. Prune entries older than 1 hour from callLog
 *   3. If len(callLog) >= maxPerHour → return error
 *   4. Append time.Now() to callLog
 */
func (r *RunwayAdapter) checkRateLimit() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)

	/* Prune expired entries */
	pruned := r.callLog[:0]
	for _, t := range r.callLog {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	r.callLog = pruned

	if len(r.callLog) >= r.maxPerHour {
		return fmt.Errorf(
			"rate limit exceeded: %d calls in the last hour (max %d)",
			len(r.callLog), r.maxPerHour,
		)
	}

	r.callLog = append(r.callLog, time.Now())
	return nil
}

/*
 * setHeaders applies the required authentication and version
 * headers to every Runway API request.
 */
func (r *RunwayAdapter) SetHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.apiKey))
	req.Header.Set("X-Runway-Version", runwayAPIVersion)
}

/* ── Runway API request/response types ────────────────────── */

/*
 * runwayCreateRequest is the JSON body for POST /v1/image_to_video.
 * Omitting promptImage enables pure text-to-video mode.
 */
type runwayCreateRequest struct {
	Model       string `json:"model"`
	PromptText  string `json:"promptText"`
	PromptImage string `json:"promptImage,omitempty"`
	Ratio       string `json:"ratio"`
	Duration    int    `json:"duration"`
}

/* runwayCreateResponse holds the task ID returned after submission. */
type runwayCreateResponse struct {
	ID string `json:"id"`
}

/*
 * runwayTaskResponse holds the full task status from GET /v1/tasks/{id}.
 * Output is an array of URLs (video downloads) when status is SUCCEEDED.
 * Failure contains an error message when status is FAILED.
 */
type runwayTaskResponse struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Output  []string `json:"output"`
	Failure *string  `json:"failure"`
}

/*
 * toGenerationResult maps the Runway task status to our
 * GenerationStatus enum and populates VideoURL or Error.
 *
 * Runway statuses:
 *   PENDING    → StatusPending
 *   THROTTLED  → StatusPending (queued due to concurrency limit)
 *   RUNNING    → StatusProcessing
 *   SUCCEEDED  → StatusCompleted (VideoURL from output[0])
 *   FAILED     → StatusFailed (Error from failure field)
 *   CANCELLED  → StatusFailed (with "task cancelled" message)
 */
func (t *runwayTaskResponse) toGenerationResult() GenerationResult {
	switch t.Status {
	case "SUCCEEDED":
		videoURL := ""
		if len(t.Output) > 0 {
			videoURL = t.Output[0]
		}
		return GenerationResult{
			Status:   StatusCompleted,
			VideoURL: videoURL,
		}

	case "FAILED":
		errMsg := "unknown failure"
		if t.Failure != nil {
			errMsg = *t.Failure
		}
		return GenerationResult{
			Status: StatusFailed,
			Error:  errMsg,
		}

	case "CANCELLED":
		return GenerationResult{
			Status: StatusFailed,
			Error:  "task cancelled",
		}

	case "RUNNING":
		return GenerationResult{
			Status: StatusProcessing,
		}

	case "PENDING", "THROTTLED":
		return GenerationResult{
			Status: StatusPending,
		}

	default:
		return GenerationResult{
			Status: StatusPending,
		}
	}
}
