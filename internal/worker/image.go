/*
 * internal/worker/image.go
 *
 * Implements the handler for the video:generate_image task.
 * Atomically picks up an unused prompt, calls the Image Provider to
 * create an anchor image, and enqueues the video:generate task.
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
	"path/filepath"

	"github.com/hereticrush/bap/internal/adapter/image"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

/*
 * HandleGenerateImageTask claims an unused prompt, generates an image,
 * saves the metadata, and enqueues the video generation task.
 */
func (p *VideoProcessor) HandleGenerateImageTask(ctx context.Context, t *asynq.Task) error {
	/* 1. Atomically create a job from an unused prompt */
	job, err := db.CreateJob(p.DB, p.Provider.Name())
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Debug("no unused prompts available for image generation, skipping")
			return nil /* Graceful exit, will be tried again by scheduler */
		}
		return fmt.Errorf("create job: %w", err)
	}

	slog.Info("starting image generation", "job_id", job.ID)

	/* 2. Build image request */
	imageDir := filepath.Join("data", "images")
	imagePath := filepath.Join(imageDir, fmt.Sprintf("%s.png", job.ID))

	req := image.GenerationRequest{
		Prompt: job.PromptTextSnapshot,
		Width:  1280,
		Height: 720,
	}

	/* 3. Call Image Provider */
	_, err = p.ImageProvider.GenerateImage(ctx, req, imagePath)
	if err != nil {
		if setErr := db.SetJobFailed(p.DB, job.ID, fmt.Sprintf("image generation: %v", err)); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", job.ID, "error", setErr)
		}
		return fmt.Errorf("generate image: %w", err)
	}

	/* 4. Update Database Metadata and Status */
	meta := map[string]interface{}{
		"image_anchors": []string{imagePath},
	}
	
	// Preserve existing metadata if any
	if job.Metadata != "" {
		var existing map[string]interface{}
		if json.Unmarshal([]byte(job.Metadata), &existing) == nil {
			for k, v := range existing {
				if k != "image_anchors" {
					meta[k] = v
				}
			}
		}
	}

	metaJSON, _ := json.Marshal(meta)
	_, err = p.DB.ExecContext(ctx, 
		"UPDATE video_jobs SET metadata = ?, status = 'IMAGE_READY', updated_at = datetime('now') WHERE id = ?", 
		metaJSON, job.ID,
	)
	if err != nil {
		return fmt.Errorf("update job metadata: %w", err)
	}

	/* 5. Enqueue Video Generation Task */
	payload, err := json.Marshal(map[string]string{"job_id": job.ID})
	if err != nil {
		return fmt.Errorf("marshal video task payload: %w", err)
	}

	videoTask := asynq.NewTask(TypeGenerateVideo, payload)
	if _, err := p.Client.EnqueueContext(ctx, videoTask); err != nil {
		return fmt.Errorf("enqueue video task: %w", err)
	}

	slog.Info("image generation completed successfully", "job_id", job.ID, "image", imagePath)
	return nil
}
