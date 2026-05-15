/*
 * internal/adapter/image/pollinations.go
 *
 * Implements AIImageProvider using the free pollinations.ai REST API.
 * It URL-encodes the prompt and directly downloads the generated image.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package image

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

/* PollinationsAdapter generates images using pollinations.ai. */
type PollinationsAdapter struct {
	client *http.Client
}

/* NewPollinationsAdapter initializes a new adapter. */
func NewPollinationsAdapter() *PollinationsAdapter {
	return &PollinationsAdapter{
		client: &http.Client{
			Timeout: 60 * time.Second, /* Image generation can take a bit */
		},
	}
}

/* Name returns the provider identifier. */
func (p *PollinationsAdapter) Name() string {
	return "POLLINATIONS"
}

/*
 * GenerateImage requests an image from pollinations.ai and writes the
 * response body directly to outputFilename.
 */
func (p *PollinationsAdapter) GenerateImage(ctx context.Context, req GenerationRequest, outputFilename string) (GenerationResult, error) {
	/* 1. Ensure output directory exists */
	outDir := filepath.Dir(outputFilename)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return GenerationResult{}, fmt.Errorf("create image output dir: %w", err)
	}

	/* 2. Build the URL. Format: https://image.pollinations.ai/prompt/{prompt}?width={w}&height={h}&nologo=true */
	encodedPrompt := url.PathEscape(req.Prompt)
	reqURL := fmt.Sprintf(
		"https://image.pollinations.ai/prompt/%s?width=%d&height=%d&nologo=true",
		encodedPrompt, req.Width, req.Height,
	)

	slog.Info("requesting image from pollinations", "url", reqURL)

	/* 3. Execute GET request */
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("create http request: %w", err)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("pollinations http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GenerationResult{}, fmt.Errorf("pollinations returned status: %s", resp.Status)
	}

	/* 4. Stream response to file */
	outFile, err := os.Create(outputFilename)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("create output image file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return GenerationResult{}, fmt.Errorf("write image stream to file: %w", err)
	}

	slog.Info("image generated successfully", "path", outputFilename)

	return GenerationResult{
		FilePath: outputFilename,
	}, nil
}
