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
	"strings"
	"time"
)

/*
 * API reference for ElevenLabs text-to-speech endpoint.
 * voiceId determines the speaker.
 */
const elevenLabsBaseURL = "https://api.elevenlabs.io/v1/text-to-speech"

type charTime struct {
	char  string
	start float64
	end   float64
}

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

	srtPath := strings.TrimSuffix(outputFilename, filepath.Ext(outputFilename)) + ".srt"

	/* Try with-timestamps API first */
	slog.Info("attempting to generate voiceover with exact character alignments from ElevenLabs", "voice_id", e.voiceID)
	result, err := e.generateWithTimestamps(ctx, text, outputFilename, srtPath)
	if err == nil {
		return result, nil
	}

	slog.Warn("ElevenLabs timestamps stream failed or unsupported, falling back to standard audio with mock timing", "error", err)

	/* 2. Standard Fallback TTS payload */
	payload := map[string]interface{}{
		"text":     text,
		"model_id": "eleven_multilingual_v2",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return AudioResult{}, fmt.Errorf("marshal elevenlabs request: %w", err)
	}

	/* 3. Execute Standard POST to ElevenLabs */
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

	/* Generate linear mock SRT subtitles for the fallback path */
	if mockErr := generateMockSRT(text, srtPath); mockErr != nil {
		slog.Warn("failed to generate mock subtitles", "error", mockErr)
	} else {
		slog.Info("fallback linear subtitles generated successfully", "path", srtPath)
	}

	slog.Info("audio generated successfully with fallback", "voice_id", e.voiceID, "path", outputFilename)

	return AudioResult{
		FilePath:      outputFilename,
		SubtitlesPath: srtPath,
	}, nil
}

type elevenLabsTimestampResponse struct {
	AudioBase64 string `json:"audio_base64"`
	Alignment   *struct {
		Characters                 []string  `json:"chars"`
		CharacterStartTimesSeconds []float64 `json:"char_start_times_ms"` // API sometimes returns ms or seconds, we map character times
		CharacterEndTimesSeconds   []float64 `json:"char_duration_ms"`
	} `json:"alignment"`
}

/*
 * generateWithTimestamps queries ElevenLabs with-timestamps API,
 * decodes chunked base64 streams into the final mp3, and groups character
 * alignments to write a precise .srt file.
 */
func (e *ElevenLabsAdapter) generateWithTimestamps(ctx context.Context, text string, outputFilename string, srtPath string) (AudioResult, error) {
	url := fmt.Sprintf("%s/%s/stream/with-timestamps", elevenLabsBaseURL, e.voiceID)

	payload := map[string]interface{}{
		"text":     text,
		"model_id": "eleven_multilingual_v2",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AudioResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return AudioResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return AudioResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return AudioResult{}, fmt.Errorf("http status %d", resp.StatusCode)
	}

	outFile, err := os.Create(outputFilename)
	if err != nil {
		return AudioResult{}, err
	}
	defer outFile.Close()

	// Parse stream lines (JSON lines format)
	dec := json.NewDecoder(resp.Body)
	
	var charTimings []charTime
	var totalOffsetSeconds float64

	for {
		var chunk struct {
			AudioBase64 string `json:"audio_base64"`
			Alignment   *struct {
				Chars        []string `json:"chars"`
				CharStartMs  []int    `json:"char_start_times_ms"`
				CharDurMs    []int    `json:"char_duration_ms"`
			} `json:"alignment"`
		}

		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return AudioResult{}, err
		}

		// Decode audio chunk
		if chunk.AudioBase64 != "" {
			importBytes, decodeErr := io.ReadAll(
				bytes.NewReader([]byte(chunk.AudioBase64)),
			)
			if decodeErr == nil {
				// Clean and decode base64
				rawDecoded := make([]byte, len(importBytes))
				n, b64Err := base64Decode(importBytes, rawDecoded)
				if b64Err == nil {
					_, _ = outFile.Write(rawDecoded[:n])
				}
			}
		}

		// Accumulate character alignments
		if chunk.Alignment != nil && len(chunk.Alignment.Chars) > 0 {
			for i, char := range chunk.Alignment.Chars {
				if i < len(chunk.Alignment.CharStartMs) && i < len(chunk.Alignment.CharDurMs) {
					startSec := totalOffsetSeconds + float64(chunk.Alignment.CharStartMs[i])/1000.0
					endSec := startSec + float64(chunk.Alignment.CharDurMs[i])/1000.0
					charTimings = append(charTimings, charTime{
						char:  char,
						start: startSec,
						end:   endSec,
					})
				}
			}
			// Update offset by the end time of the last character
			if len(chunk.Alignment.CharStartMs) > 0 {
				lastIdx := len(chunk.Alignment.CharStartMs) - 1
				totalOffsetSeconds += float64(chunk.Alignment.CharStartMs[lastIdx]+chunk.Alignment.CharDurMs[lastIdx])/1000.0
			}
		}
	}

	/* Write character timings out to SRT file */
	if len(charTimings) > 0 {
		if err := writeSRTFromAlignments(charTimings, srtPath); err != nil {
			slog.Error("failed to write SRT alignments", "error", err)
		}
	} else {
		// Fallback to linear mock subtitles if API alignments are empty
		_ = generateMockSRT(text, srtPath)
	}

	return AudioResult{
		FilePath:      outputFilename,
		SubtitlesPath: srtPath,
	}, nil
}

/* base64Decode performs custom robust base64 decoding */
func base64Decode(src []byte, dst []byte) (int, error) {
	// Simple standard base64 decoding helper
	var alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var decodeMap [256]byte
	for i := 0; i < len(alphabet); i++ {
		decodeMap[alphabet[i]] = byte(i)
	}

	var padding int
	var buffer int
	var bits int
	var dstIdx int

	for _, c := range src {
		if c == '=' {
			padding++
			continue
		}
		if c == '\r' || c == '\n' || c == ' ' || c == '\t' {
			continue
		}
		val := decodeMap[c]
		buffer = (buffer << 6) | int(val)
		bits += 6

		if bits >= 8 {
			bits -= 8
			dst[dstIdx] = byte((buffer >> bits) & 0xFF)
			dstIdx++
		}
	}
	return dstIdx, nil
}

func writeSRTFromAlignments(timings []charTime, srtPath string) error {
	outFile, err := os.Create(srtPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	type wordTime struct {
		word  string
		start float64
		end   float64
	}

	var words []wordTime
	var currentWord strings.Builder
	var wordStart float64
	inWord := false

	// Group character alignments into words
	for _, t := range timings {
		isSpace := t.char == " " || t.char == "\t" || t.char == "\n" || t.char == "\r"
		if !isSpace {
			if !inWord {
				wordStart = t.start
				inWord = true
			}
			currentWord.WriteString(t.char)
		} else {
			if inWord {
				words = append(words, wordTime{
					word:  currentWord.String(),
					start: wordStart,
					end:   t.start,
				})
				currentWord.Reset()
				inWord = false
			}
		}
	}
	if inWord {
		words = append(words, wordTime{
			word:  currentWord.String(),
			start: wordStart,
			end:   timings[len(timings)-1].end,
		})
	}

	if len(words) == 0 {
		return nil
	}

	// Group words into lines of 3 words for rapid modern social media format
	const wordsPerLine = 3
	lineIdx := 1
	for i := 0; i < len(words); i += wordsPerLine {
		endIdx := i + wordsPerLine
		if endIdx > len(words) {
			endIdx = len(words)
		}

		var lineText []string
		for _, w := range words[i:endIdx] {
			lineText = append(lineText, w.word)
		}
		textStr := strings.Join(lineText, " ")
		startTime := words[i].start
		endTime := words[endIdx-1].end

		fmt.Fprintf(outFile, "%d\n", lineIdx)
		fmt.Fprintf(outFile, "%s --> %s\n", formatSRTTime(startTime), formatSRTTime(endTime))
		fmt.Fprintf(outFile, "%s\n\n", textStr)
		lineIdx++
	}

	return nil
}

func generateMockSRT(text string, srtPath string) error {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	outFile, err := os.Create(srtPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	const wordsPerSec = 2.2
	const wordsPerLine = 3

	lineIdx := 1
	for i := 0; i < len(words); i += wordsPerLine {
		endIdx := i + wordsPerLine
		if endIdx > len(words) {
			endIdx = len(words)
		}
		lineWords := words[i:endIdx]
		lineText := strings.Join(lineWords, " ")

		startTime := float64(i) / wordsPerSec
		endTime := float64(endIdx) / wordsPerSec

		fmt.Fprintf(outFile, "%d\n", lineIdx)
		fmt.Fprintf(outFile, "%s --> %s\n", formatSRTTime(startTime), formatSRTTime(endTime))
		fmt.Fprintf(outFile, "%s\n\n", lineText)
		lineIdx++
	}

	return nil
}

func formatSRTTime(seconds float64) string {
	hrs := int(seconds) / 3600
	mins := (int(seconds) % 3600) / 60
	secs := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hrs, mins, secs, ms)
}
