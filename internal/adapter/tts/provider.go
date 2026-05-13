/*
 * internal/adapter/tts/provider.go
 *
 * Defines the TTSProvider interface and associated types.
 * Any Text-To-Speech service (ElevenLabs, OpenAI, etc.) is integrated
 * by implementing this contract.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package tts

import "context"

/*
 * AudioResult holds the outcome of a TTS generation call.
 */
type AudioResult struct {
	FilePath string /* Absolute or relative path to the generated audio file */
}

/*
 * TTSProvider is the adapter interface.
 * Any audio generation service can be integrated by implementing
 * this contract. Each adapter receives its provider-specific
 * configuration via dependency injection through its constructor.
 *
 * All methods accept a context.Context to support timeouts,
 * deadlines, and cancellation propagation from the Asynq worker.
 */
type TTSProvider interface {
	/* Name returns the provider identifier (e.g., "ELEVENLABS"). */
	Name() string

	/*
	 * GenerateAudio submits a TTS request to the AI provider and
	 * downloads the resulting audio stream to outputFilename.
	 */
	GenerateAudio(ctx context.Context, text string, outputFilename string) (AudioResult, error)
}
