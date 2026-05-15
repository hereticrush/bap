/*
 * internal/adapter/image/provider.go
 *
 * Defines the generic interface for AI Image Providers used to generate
 * anchor images for the video pipeline.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package image

import "context"

/* GenerationRequest contains parameters for generating an image. */
type GenerationRequest struct {
	Prompt string
	Width  int
	Height int
}

/* GenerationResult contains the final output path of the downloaded image. */
type GenerationResult struct {
	FilePath string
}

/* AIImageProvider abstracts the underlying image generation service. */
type AIImageProvider interface {
	Name() string
	GenerateImage(ctx context.Context, req GenerationRequest, outputFilename string) (GenerationResult, error)
}
