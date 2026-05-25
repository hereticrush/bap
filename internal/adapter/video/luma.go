/*
 * internal/adapter/video/luma.go
 *
 * LumaAdapter implements AIVideoProvider and AssetUploader by calling
 * the Luma AI Dream Machine REST API (api.lumalabs.ai).
 *
 * Uses stdlib net/http only — no SDK dependency.
 *
 * Safety features:
 *   - Sliding-window hourly rate limiter (configurable via maxPerHour)
 *   - Full API error JSON logged and propagated on any non-200 response
 *   - Dynamic keyframe image uploads using the injected StorageProvider
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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hereticrush/bap/internal/adapter/storage"
)

/* Luma REST API base URL. */
var lumaBaseURL = "https://api.lumalabs.ai/dream-machine/v1"

/* LumaAdapter generates videos via the Luma AI Dream Machine REST API. */
type LumaAdapter struct {
	apiKey     string
	model      string
	maxPerHour int
	callLog    []time.Time
	mu         sync.Mutex
	client     *http.Client
	storage    storage.StorageProvider
}

/*
 * NewLumaAdapter creates a LumaAdapter with the given API key,
 * model name, hourly rate limit cap, and cloud storage provider.
 */
func NewLumaAdapter(apiKey, model string, maxPerHour int, storage storage.StorageProvider) *LumaAdapter {
	return &LumaAdapter{
		apiKey:     apiKey,
		model:      model,
		maxPerHour: maxPerHour,
		callLog:    make([]time.Time, 0, maxPerHour),
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		storage: storage,
	}
}

/* Name returns the provider identifier. */
func (l *LumaAdapter) Name() string {
	return "LUMA"
}

/*
 * GenerateVideo submits a video generation request to Luma Dream Machine.
 * Enforces the hourly rate limit before each call and logs full API
 * error JSON on failure.
 */
func (l *LumaAdapter) GenerateVideo(ctx context.Context, req GenerationRequest) (string, error) {
	/* Step 1: Enforce hourly rate limit */
	if err := l.checkRateLimit(); err != nil {
		return "", err
	}

	/* Step 2: Build the request payload with mapped aspect ratio */
	ratio := mapAspectRatio(req.AspectRatio)

	var keyframes interface{}
	if len(req.ImageURLs) > 0 {
		keyframes = map[string]interface{}{
			"frame0": map[string]string{
				"type": "image",
				"url":  req.ImageURLs[0],
			},
		}
	}

	payload := lumaCreateRequest{
		Prompt:      req.Prompt,
		Model:       l.model,
		AspectRatio: ratio,
		Keyframes:   keyframes,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	/* Step 3: POST to Luma Generations API */
	url := fmt.Sprintf("%s/generations", lumaBaseURL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	l.setHeaders(httpReq)

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("luma HTTP call: %w", err)
	}
	defer resp.Body.Close()

	/* Read the full response body */
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	/* Non-200: log full JSON error and return wrapped error */
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("luma API error",
			"status_code", resp.StatusCode,
			"model", l.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("luma API returned %d: %s", resp.StatusCode, string(respBody))
	}

	/* Step 4: Parse the task ID from the response */
	var createResp lumaCreateResponse
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return "", fmt.Errorf("parse luma response: %w", err)
	}

	if createResp.ID == "" {
		slog.Error("luma returned empty task ID",
			"model", l.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("luma returned empty task ID")
	}

	slog.Info("luma video generation submitted",
		"task_id", createResp.ID,
		"model", l.model,
		"aspect_ratio", ratio,
	)

	return createResp.ID, nil
}

/*
 * CheckStatus polls Luma for the current state of a generation
 * task identified by taskID.
 */
func (l *LumaAdapter) CheckStatus(ctx context.Context, taskID string) (GenerationResult, error) {
	url := fmt.Sprintf("%s/generations/%s", lumaBaseURL, taskID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("create request: %w", err)
	}
	l.setHeaders(httpReq)

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("luma HTTP call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("luma task status error",
			"status_code", resp.StatusCode,
			"task_id", taskID,
			"response_body", string(respBody),
		)
		return GenerationResult{}, fmt.Errorf("luma task status returned %d: %s", resp.StatusCode, string(respBody))
	}

	var taskResp lumaTaskResponse
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		return GenerationResult{}, fmt.Errorf("parse task response: %w", err)
	}

	return taskResp.toGenerationResult(), nil
}

/*
 * UploadImage implements the AssetUploader interface by uploading
 * the local image to cloud storage so it is publicly accessible for Luma.
 */
func (l *LumaAdapter) UploadImage(ctx context.Context, localPath string) (string, error) {
	if l.storage == nil {
		return "", fmt.Errorf("storage provider not configured for luma keyframe uploads")
	}

	slog.Info("luma adapter uploading image anchor via storage provider", "path", localPath, "storage", l.storage.Name())
	url, err := l.storage.UploadFile(ctx, localPath, "image/png")
	if err != nil {
		return "", fmt.Errorf("luma keyframe upload: %w", err)
	}

	/* In credless/stub environment, return a compatible string reference */
	if strings.HasPrefix(url, "local://") {
		filename := filepath.Base(localPath)
		return fmt.Sprintf("luma://ephemeral/%s", filename), nil
	}

	return url, nil
}

/* checkRateLimit enforces the sliding-window hourly rate limit. */
func (l *LumaAdapter) checkRateLimit() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)

	/* Prune expired entries */
	pruned := l.callLog[:0]
	for _, t := range l.callLog {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	l.callLog = pruned

	if len(l.callLog) >= l.maxPerHour {
		return fmt.Errorf("rate limit exceeded: %d calls in the last hour (max %d)", len(l.callLog), l.maxPerHour)
	}

	l.callLog = append(l.callLog, time.Now())
	return nil
}

/* setHeaders applies authorization headers to every Luma API request. */
func (l *LumaAdapter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", l.apiKey))
}

/* mapAspectRatio converts arbitrary aspect ratios to Luma-supported ones. */
func mapAspectRatio(ratio string) string {
	switch ratio {
	case "1280:720", "16:9":
		return "16:9"
	case "720:1280", "9:16":
		return "9:16"
	case "1:1":
		return "1:1"
	case "4:3":
		return "4:3"
	case "3:4":
		return "3:4"
	case "21:9":
		return "21:9"
	case "9:21":
		return "9:21"
	default:
		return "16:9"
	}
}

/* ── Luma API request/response types ──────────────────────── */

type lumaCreateRequest struct {
	Prompt      string      `json:"prompt"`
	Model       string      `json:"model"`
	AspectRatio string      `json:"aspect_ratio"`
	Keyframes   interface{} `json:"keyframes,omitempty"`
}

type lumaCreateResponse struct {
	ID string `json:"id"`
}

type lumaTaskResponse struct {
	ID            string            `json:"id"`
	State         string            `json:"state"`
	Assets        *lumaAssets       `json:"assets"`
	FailureReason *string           `json:"failure_reason"`
}

type lumaAssets struct {
	Video string `json:"video"`
}

/*
 * toGenerationResult maps the Luma task state to our GenerationStatus enum.
 * States:
 *   queued   → StatusPending
 *   dreaming → StatusProcessing
 *   completed→ StatusCompleted (VideoURL from assets.video)
 *   failed   → StatusFailed (Error from failure_reason)
 */
func (t *lumaTaskResponse) toGenerationResult() GenerationResult {
	switch t.State {
	case "completed":
		videoURL := ""
		if t.Assets != nil {
			videoURL = t.Assets.Video
		}
		return GenerationResult{
			Status:   StatusCompleted,
			VideoURL: videoURL,
		}

	case "failed":
		errMsg := "unknown failure"
		if t.FailureReason != nil {
			errMsg = *t.FailureReason
		}
		return GenerationResult{
			Status: StatusFailed,
			Error:  errMsg,
		}

	case "dreaming":
		return GenerationResult{
			Status: StatusProcessing,
		}

	case "queued":
		return GenerationResult{
			Status: StatusPending,
		}

	default:
		return GenerationResult{
			Status: StatusPending,
		}
	}
}
