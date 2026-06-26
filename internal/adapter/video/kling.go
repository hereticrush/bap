// Package video provides AI video generation adapters.
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

/* Kling API constants */
var klingBaseURL = "https://api-singapore.klingai.com"

/* KlingAdapter implements AIVideoProvider and AssetUploader by calling
 * the Kling AI REST API (api-singapore.klingai.com).
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
type KlingAdapter struct {
	apiKey     string
	model      string
	maxPerHour int
	callLog    []time.Time
	mu         sync.Mutex
	client     *http.Client
	storage    storage.StorageProvider
}

func NewKlingAdapter(apiKey, model string, maxPerHour int, storage storage.StorageProvider) *KlingAdapter {
	return &KlingAdapter{
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

func (k *KlingAdapter) Name() string {
	return "KLING"
}

func (k *KlingAdapter) SetHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+k.apiKey)
}

func (k *KlingAdapter) GenerateVideo(ctx context.Context, req GenerationRequest) (string, error) {
	/* Step 1: Enforce hourly rate limit */
	if err := k.checkRateLimit(); err != nil {
		return "", err
	}

	/* Step 2: Build the request payload and select endpoint */
	duration := req.Duration
	if duration == 0 {
		duration = 5
	}

	ratio := mapAspectRatio(req.AspectRatio)

	var url string
	var body []byte
	var err error

	if len(req.ImageURLs) > 0 {
		url = fmt.Sprintf("%s/v1/videos/image2video", klingBaseURL)
		payload := klingImage2VideoRequest{
			ModelName:   k.model,
			Image:       req.ImageURLs[0],
			Prompt:      req.Prompt,
			Duration:    duration,
			AspectRatio: ratio,
		}
		body, err = json.Marshal(payload)
	} else {
		url = fmt.Sprintf("%s/v1/videos/text2video", klingBaseURL)
		payload := klingText2VideoRequest{
			ModelName:   k.model,
			Prompt:      req.Prompt,
			Duration:    duration,
			AspectRatio: ratio,
		}
		body, err = json.Marshal(payload)
	}

	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	/* Step 3: POST to Kling API */
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	k.SetHeaders(httpReq)

	resp, err := k.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("kling HTTP call: %w", err)
	}
	defer resp.Body.Close()

	/* Read the full response body */
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	/* Non-200: log full JSON error and return wrapped error */
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("kling API error",
			"status_code", resp.StatusCode,
			"model", k.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("kling API returned %d: %s", resp.StatusCode, string(respBody))
	}

	/* Step 4: Parse the task ID from the response */
	var createResp klingCreateResponse
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return "", fmt.Errorf("parse kling response: %w", err)
	}

	if createResp.Code != 0 {
		slog.Error("kling returned error code",
			"code", createResp.Code,
			"message", createResp.Message,
			"model", k.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("kling API error: %s (code %d)", createResp.Message, createResp.Code)
	}

	if createResp.Data.TaskID == "" {
		slog.Error("kling returned empty task ID",
			"model", k.model,
			"response_body", string(respBody),
		)
		return "", fmt.Errorf("kling returned empty task ID")
	}

	slog.Info("kling video generation submitted",
		"task_id", createResp.Data.TaskID,
		"model", k.model,
		"duration", duration,
		"ratio", ratio,
	)

	return createResp.Data.TaskID, nil
}

func (k *KlingAdapter) CheckStatus(ctx context.Context, taskID string) (GenerationResult, error) {
	url := fmt.Sprintf("%s/v1/videos/status/%s", klingBaseURL, taskID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("create request: %w", err)
	}
	k.SetHeaders(httpReq)

	resp, err := k.client.Do(httpReq)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("kling HTTP call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("kling task status error",
			"status_code", resp.StatusCode,
			"task_id", taskID,
			"response_body", string(respBody),
		)
		return GenerationResult{}, fmt.Errorf("kling task status returned %d: %s", resp.StatusCode, string(respBody))
	}

	var taskResp klingTaskResponse
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		return GenerationResult{}, fmt.Errorf("parse task response: %w", err)
	}

	return taskResp.toGenerationResult(), nil
}

func (k *KlingAdapter) UploadImage(ctx context.Context, localPath string) (string, error) {
	if k.storage == nil {
		return "", fmt.Errorf("storage provider not configured for kling keyframe uploads")
	}

	slog.Info("kling adapter uploading image anchor via storage provider", "path", localPath, "storage", k.storage.Name())
	url, err := k.storage.UploadFile(ctx, localPath, "image/png")
	if err != nil {
		return "", fmt.Errorf("kling keyframe upload: %w", err)
	}

	/* In credless/stub environment, return a compatible string reference */
	if strings.HasPrefix(url, "local://") {
		filename := filepath.Base(localPath)
		return fmt.Sprintf("kling://ephemeral/%s", filename), nil
	}

	return url, nil
}

func (k *KlingAdapter) checkRateLimit() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Hour)

	/* Prune expired entries */
	pruned := k.callLog[:0]
	for _, t := range k.callLog {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	k.callLog = pruned
	if len(k.callLog) >= k.maxPerHour {
		return fmt.Errorf("kling: rate limit exceeded. max %d calls per hour", k.maxPerHour)
	}

	k.callLog = append(k.callLog, now)
	return nil
}

/* mapAspectRatio converts arbitrary aspect ratios to Kling-supported ones. */
/*func mapAspectRatio(ratio string) string {
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
}*/

/* ── Kling API request/response types ──────────────────────── */

type klingText2VideoRequest struct {
	ModelName      string  `json:"model_name"`
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Duration       int     `json:"duration,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	CFGScale       float64 `json:"cfg_scale,omitempty"`
}

type klingImage2VideoRequest struct {
	ModelName      string  `json:"model_name"`
	Image          string  `json:"image"`
	Prompt         string  `json:"prompt,omitempty"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Duration       int     `json:"duration,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
}

type klingCreateResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		CreatedAt  int64  `json:"created_at"`
	} `json:"data"`
}

type klingTaskResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Data      struct {
		TaskID       string `json:"task_id"`
		TaskStatus   string `json:"task_status"`
		ErrorMessage string `json:"error_message,omitempty"`
		TaskResult   *struct {
			Videos []struct {
				ID       string `json:"id"`
				URL      string `json:"url"`
				Duration string `json:"duration"`
			} `json:"videos"`
		} `json:"task_result,omitempty"`
	} `json:"data"`
}

func (t *klingTaskResponse) toGenerationResult() GenerationResult {
	if t.Code != 0 {
		return GenerationResult{
			Status: StatusFailed,
			Error:  fmt.Sprintf("API error code %d: %s", t.Code, t.Message),
		}
	}

	switch t.Data.TaskStatus {
	case "succeed":
		videoURL := ""
		if t.Data.TaskResult != nil && len(t.Data.TaskResult.Videos) > 0 {
			videoURL = t.Data.TaskResult.Videos[0].URL
		}
		return GenerationResult{
			Status:   StatusCompleted,
			VideoURL: videoURL,
		}

	case "failed":
		errMsg := t.Data.ErrorMessage
		if errMsg == "" {
			errMsg = t.Message
		}
		if errMsg == "" {
			errMsg = "unknown failure"
		}
		return GenerationResult{
			Status: StatusFailed,
			Error:  errMsg,
		}

	case "processing":
		return GenerationResult{
			Status: StatusProcessing,
		}

	case "submitted":
		return GenerationResult{
			Status: StatusPending,
		}

	default:
		return GenerationResult{
			Status: StatusPending,
		}
	}
}