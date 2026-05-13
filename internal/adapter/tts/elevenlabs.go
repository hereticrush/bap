/*
 * internal/adapter/tts/elevenlabs.go
 *
 * ElevenLabsAdapter implements TTSProvider by calling the
 * ElevenLabs REST API to generate high-quality voiceovers.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

/*
 * API reference for ElevenLabs text-to-speech endpoint.
 * voiceId determines the speaker.
 */
const elevenLabsBaseURL = "https://api.elevenlabs.io/v1/text-to-speech"

/* ElevenLabsAdapter generates audio via ElevenLabs REST API. */
type ElevenLabsAdapter struct {
	apiKey  string
	voiceID string
	client  *http.Client
}

/*
 * NewElevenLabsAdapter creates an adapter with the given API key
 * and the specific voice identifier to be used for generation.
 */
func NewElevenLabsAdapter(apiKey, voiceID string) *ElevenLabsAdapter {
	return &ElevenLabsAdapter{
		apiKey:  apiKey,
		voiceID: voiceID,
		client: &http.Client{
			Timeout: 60 * time.Second, /* Audio generation usually takes a few seconds */
		},
	}
}

/* Name returns the provider identifier. */
func (e *ElevenLabsAdapter) Name() string {
	return "ELEVENLABS"
}

/*
 * GenerateAudio converts text to speech and downloads the MP3 stream
 * directly to the specified outputFilename on disk.
 */
func (e *ElevenLabsAdapter) GenerateAudio(ctx context.Context, text string, outputFilename string) (AudioResult, error) {
	/* 1. Ensure the parent directory exists */
	outDir := filepath.Dir(outputFilename)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return AudioResult{}, fmt.Errorf("create audio output dir: %w", err)
	}

	/* 2. Build the request payload */
	payload := map[string]interface{}{
		"text": text,
		"model_id": "eleven_multilingual_v2",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return AudioResult{}, fmt.Errorf("marshal elevenlabs request: %w", err)
	}

	/* 3. Execute POST to ElevenLabs */
	url := fmt.Sprintf("%s/%s", elevenLabsBaseURL, e.voiceID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return AudioResult{}, fmt.Errorf("create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "audio/mpeg")
	httpReq.Header.Set("xi-api-key", e.apiKey)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return AudioResult{}, fmt.Errorf("elevenlabs http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		slog.Error("elevenlabs api error",
			"status_code", resp.StatusCode,
			"response_body", string(errBody),
		)
		return AudioResult{}, fmt.Errorf("elevenlabs returned %d: %s", resp.StatusCode, string(errBody))
	}

	/* 4. Stream response body directly to a file */
	outFile, err := os.Create(outputFilename)
	if err != nil {
		return AudioResult{}, fmt.Errorf("create output audio file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return AudioResult{}, fmt.Errorf("write audio stream to file: %w", err)
	}

	slog.Info("audio generated successfully", "voice_id", e.voiceID, "path", outputFilename)

	return AudioResult{
		FilePath: outputFilename,
	}, nil
}
