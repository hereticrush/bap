/*
 * internal/worker/audio.go
 *
 * Implements the handler for the video:add_audio task.
 * Sequentially generates TTS audio via ElevenLabs and merges it
 * with the previously downloaded video using FFmpeg.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

/* HandleAddAudioTask processes the audio generation and merging step. */
func (p *VideoProcessor) HandleAddAudioTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	jobID := payload.JobID
	slog.Info("processing add audio task", "job_id", jobID)

	/* 1. Fetch prompt snapshot and metadata for TTS text */
	var promptSnapshot string
	var metadataJSON sql.NullString
	err := p.DB.QueryRow(
		"SELECT prompt_text_snapshot, metadata FROM video_jobs WHERE id = ?", jobID,
	).Scan(&promptSnapshot, &metadataJSON)
	if err != nil {
		return fmt.Errorf("fetch job: %w", err)
	}

	textToRead := db.TTSText(metadataJSON.String, promptSnapshot)
	if textToRead == "" {
		return fmt.Errorf("no text available for TTS (job_id=%s)", jobID)
	}

	/* 2. Generate Audio via ElevenLabs */
	audioDir := filepath.Join("data", "audio")
	audioPath := filepath.Join(audioDir, fmt.Sprintf("%s.mp3", jobID))
	
	audioRes, err := p.TTSProvider.GenerateAudio(ctx, textToRead, audioPath)
	if err != nil {
		if setErr := db.SetJobFailed(p.DB, jobID, fmt.Sprintf("audio generation: %v", err)); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", jobID, "error", setErr)
		}
		return fmt.Errorf("generate audio: %w", err)
	}

	/* 3. Execute FFmpeg to merge Audio and Video */
	videoPath := filepath.Join(p.VideoOutputDir, fmt.Sprintf("%s.mp4", jobID))
	finalPath := filepath.Join(p.VideoOutputDir, fmt.Sprintf("%s_final.mp4", jobID))

	// ffmpeg -y -i video.mp4 -i audio.mp3 -c:v copy -c:a aac -map 0:v:0 -map 1:a:0 final.mp4
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",                   // Overwrite output
		"-i", videoPath,        // Input 1: The video
		"-i", audioRes.FilePath, // Input 2: The audio
		"-c:v", "copy",         // Copy video stream without re-encoding
		"-c:a", "aac",          // Encode audio to AAC
		"-map", "0:v:0",        // Use video from first input
		"-map", "1:a:0",        // Use audio from second input
		"-shortest",            // Finish encoding when the shortest input stream ends
		finalPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if setErr := db.SetJobFailed(p.DB, jobID, fmt.Sprintf("ffmpeg merge: %v", err)); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", jobID, "error", setErr)
		}
		return fmt.Errorf("ffmpeg execution failed: %w, output: %s", err, string(output))
	}

	/* Replace original video with final merged video for simplicity downstream */
	if err := os.Rename(finalPath, videoPath); err != nil {
		return fmt.Errorf("rename final video: %w", err)
	}

	/* Upload final merged video to cloud storage */
	slog.Info("uploading final merged video to cloud storage", "job_id", jobID, "file", videoPath)
	s3URL, err := p.StorageProvider.UploadFile(ctx, videoPath, "video/mp4")
	if err != nil {
		slog.Error("failed to upload final merged video to cloud storage", "job_id", jobID, "error", err)
		return fmt.Errorf("upload final merged video: %w", err)
	}

	/* 4. Record local path in metadata; mark COMPLETED with final cloud storage URL */
	if err := db.MergeJobMetadata(p.DB, jobID, map[string]interface{}{
		db.MetadataKeyLocalVideoPath: videoPath,
	}); err != nil {
		return fmt.Errorf("update job metadata: %w", err)
	}

	_, err = p.DB.ExecContext(ctx,
		`UPDATE video_jobs
		 SET status = 'COMPLETED', cloud_storage_url = ?, updated_at = datetime('now'), completed_at = datetime('now')
		 WHERE id = ?`,
		s3URL, jobID,
	)
	if err != nil {
		return fmt.Errorf("set job completed and cloud URL: %w", err)
	}

	/* 5. Enqueue Publish Task */
	publishTask := asynq.NewTask(TypePublishVideo, t.Payload())
	if _, err := p.Client.EnqueueContext(ctx, publishTask); err != nil {
		return fmt.Errorf("enqueue publish task: %w", err)
	}

	slog.Info("audio successfully merged and job completed with cloud storage offload", "job_id", jobID, "s3_url", s3URL)
	return nil
}
