/*
 * internal/adapter/prompt/gemini.go
 *
 * GeminiAdapter implements AIPromptBuilder by calling the
 * Google Gemini REST API (generativelanguage.googleapis.com).
 *
 * Uses stdlib net/http only — no SDK dependency.
 *
 * Safety features:
 *   - Sliding-window hourly rate limiter (configurable via maxPerHour)
 *   - Full API error JSON logged and propagated on any non-200 response
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package prompt

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
)

/* Gemini REST API base URL (v1beta — stable for generateContent). */
const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"

/* GeminiAdapter enriches prompts via the Gemini REST API. */
type GeminiAdapter struct {
	apiKey     string
	model      string
	maxPerHour int
	callLog    []time.Time
	mu         sync.Mutex
	client     *http.Client
}

/*
 * NewGeminiAdapter creates a GeminiAdapter with the given API key,
 * model name, and hourly rate limit cap.
 *
 * Parameters:
 *   apiKey     — Gemini API key for authentication
 *   model      — Model identifier (e.g., "gemini-2.5-flash")
 *   maxPerHour — Maximum API calls allowed per rolling hour
 */
func NewGeminiAdapter(apiKey, model string, maxPerHour int) *GeminiAdapter {
	return &GeminiAdapter{
		apiKey:     apiKey,
		model:      model,
		maxPerHour: maxPerHour,
		callLog:    make([]time.Time, 0, maxPerHour),
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

/* Name returns the adapter identifier. */
func (g *GeminiAdapter) Name() string {
	return "GEMINI"
}

/*
 * BuildPrompt enriches a single seed prompt by calling the
 * Gemini generateContent endpoint. Enforces the hourly rate
 * limit before each call and logs full API error JSON on failure.
 *
 * Flow:
 *   1. Check hourly rate limit (sliding window)
 *   2. Build the generateContent JSON payload
 *   3. POST to Gemini REST API
 *   4. Parse response — extract enriched text + token count
 *   5. Return PromptBuildResult or wrapped error
 */
func (g *GeminiAdapter) BuildPrompt(ctx context.Context, req PromptBuildRequest) (PromptBuildResult, error) {
	/* Step 1: Enforce hourly rate limit */
	if err := g.checkRateLimit(); err != nil {
		return PromptBuildResult{}, err
	}

	/* Step 2: Build the request payload */
	payload := g.buildPayload(req)

	body, err := json.Marshal(payload)
	if err != nil {
		return PromptBuildResult{}, fmt.Errorf("marshal request: %w", err)
	}

	/* Step 3: POST to Gemini API */
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, g.model, g.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return PromptBuildResult{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return PromptBuildResult{}, fmt.Errorf("gemini HTTP call: %w", err)
	}
	defer resp.Body.Close()

	/* Read the full response body (needed for both success and error paths) */
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return PromptBuildResult{}, fmt.Errorf("read response body: %w", err)
	}

	/* Non-200: log full JSON error and return wrapped error */
	if resp.StatusCode != http.StatusOK {
		slog.Error("gemini API error",
			"status_code", resp.StatusCode,
			"model", g.model,
			"response_body", string(respBody),
		)
		return PromptBuildResult{}, fmt.Errorf(
			"gemini API returned %d: %s", resp.StatusCode, string(respBody),
		)
	}

	/* Step 4: Parse the successful response */
	var geminiResp geminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return PromptBuildResult{}, fmt.Errorf("parse gemini response: %w", err)
	}

	enrichedText, err := geminiResp.extractText()
	if err != nil {
		slog.Error("gemini response extraction failed",
			"model", g.model,
			"response_body", string(respBody),
		)
		return PromptBuildResult{}, err
	}

	tokensUsed := geminiResp.UsageMetadata.TotalTokenCount

	slog.Info("gemini prompt enriched",
		"model", g.model,
		"tokens_used", tokensUsed,
		"enriched_length", len(enrichedText),
	)

	return PromptBuildResult{
		EnrichedPrompt: enrichedText,
		TokensUsed:     tokensUsed,
	}, nil
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
func (g *GeminiAdapter) checkRateLimit() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)

	/* Prune expired entries */
	pruned := g.callLog[:0]
	for _, t := range g.callLog {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	g.callLog = pruned

	if len(g.callLog) >= g.maxPerHour {
		return fmt.Errorf(
			"rate limit exceeded: %d calls in the last hour (max %d)",
			len(g.callLog), g.maxPerHour,
		)
	}

	g.callLog = append(g.callLog, time.Now())
	return nil
}

/*
 * buildPayload constructs the Gemini generateContent JSON structure.
 * Maps the PromptBuildRequest fields into the Gemini API format:
 *   - SystemPrompt → systemInstruction.parts[0].text
 *   - SeedPrompt + TargetProvider + Metadata → user content
 */
func (g *GeminiAdapter) buildPayload(req PromptBuildRequest) geminiRequest {
	/* Build the user message combining seed, target, and metadata */
	userText := fmt.Sprintf(
		"Seed prompt: %s\nTarget video provider: %s",
		req.SeedPrompt, req.TargetProvider,
	)

	/* Append metadata hints if present */
	for key, val := range req.Metadata {
		userText += fmt.Sprintf("\n%s: %s", key, val)
	}

	payload := geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{Text: userText},
				},
			},
		},
	}

	/* Include system instruction if provided */
	if req.SystemPrompt != "" {
		payload.SystemInstruction = &geminiContent{
			Parts: []geminiPart{
				{Text: req.SystemPrompt},
			},
		}
	}

	return payload
}

/* ── Gemini API request/response types ────────────────────── */

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate  `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

/*
 * extractText pulls the text from candidates[0].content.parts[0].text.
 * Returns an error if the response structure is empty or unexpected.
 */
func (r *geminiResponse) extractText() (string, error) {
	if len(r.Candidates) == 0 {
		return "", fmt.Errorf("gemini returned 0 candidates")
	}
	parts := r.Candidates[0].Content.Parts
	if len(parts) == 0 {
		return "", fmt.Errorf("gemini candidate has 0 parts")
	}
	text := parts[0].Text
	if text == "" {
		return "", fmt.Errorf("gemini candidate text is empty")
	}
	return text, nil
}
