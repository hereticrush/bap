/*
 * internal/worker/pipeline.go
 *
 * Implements the handler for the video:start_pipeline task.
 * Claims a prompt, optionally generates and uploads an image anchor,
 * then enqueues video:generate.
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
 * HandleStartPipelineTask claims an unused prompt and starts the video pipeline.
 * Image anchors are optional (per-seed use_image_anchor or ENABLE_IMAGE_ANCHORS default).
 */
func (p *VideoProcessor) HandleStartPipelineTask(ctx context.Context, t *asynq.Task) error {
	job, err := db.CreateJob(p.DB, p.Provider.Name())
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Debug("no unused prompts available, skipping pipeline start")
			return nil
		}
		return fmt.Errorf("create job: %w", err)
	}

	useAnchor := db.UseImageAnchor(job.Metadata, p.DefaultImageAnchors)
	slog.Info("pipeline started", "job_id", job.ID, "use_image_anchor", useAnchor)

	if !useAnchor {
		return p.enqueueGenerateVideo(ctx, job.ID)
	}

	return p.runImageAnchorPipeline(ctx, job)
}

/*
 * runImageAnchorPipeline generates a local anchor, uploads to Runway, and enqueues generate.
 */
func (p *VideoProcessor) runImageAnchorPipeline(ctx context.Context, job *db.VideoJob) error {
	if p.Uploader == nil {
		if setErr := db.SetJobFailed(p.DB, job.ID, "image anchors enabled but no asset uploader configured (requires RUNWAY)"); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", job.ID, "error", setErr)
		}
		return fmt.Errorf("asset uploader not configured")
	}

	imageDir := filepath.Join("data", "images")
	imagePath := filepath.Join(imageDir, fmt.Sprintf("%s.png", job.ID))

	req := image.GenerationRequest{
		Prompt: job.PromptTextSnapshot,
		Width:  1280,
		Height: 720,
	}

	if _, err := p.ImageProvider.GenerateImage(ctx, req, imagePath); err != nil {
		if setErr := db.SetJobFailed(p.DB, job.ID, fmt.Sprintf("image generation: %v", err)); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", job.ID, "error", setErr)
		}
		return fmt.Errorf("generate image: %w", err)
	}

	runwayURI, err := p.Uploader.UploadImage(ctx, imagePath)
	if err != nil {
		if setErr := db.SetJobFailed(p.DB, job.ID, fmt.Sprintf("runway image upload: %v", err)); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", job.ID, "error", setErr)
		}
		return fmt.Errorf("upload image: %w", err)
	}

	patch := map[string]interface{}{
		db.MetadataKeyImageAnchors:      []string{runwayURI},
		db.MetadataKeyImageAnchorsLocal: []string{imagePath},
	}
	if err := db.MergeJobMetadata(p.DB, job.ID, patch); err != nil {
		return fmt.Errorf("update job metadata: %w", err)
	}

	_, err = p.DB.ExecContext(ctx,
		`UPDATE video_jobs SET status = 'IMAGE_READY', updated_at = datetime('now') WHERE id = ?`,
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("set image ready: %w", err)
	}

	if err := p.enqueueGenerateVideo(ctx, job.ID); err != nil {
		return err
	}

	slog.Info("image anchor pipeline complete",
		"job_id", job.ID,
		"runway_uri", runwayURI,
		"local_path", imagePath,
	)
	return nil
}

func (p *VideoProcessor) enqueueGenerateVideo(ctx context.Context, jobID string) error {
	payload, err := json.Marshal(map[string]string{"job_id": jobID})
	if err != nil {
		return fmt.Errorf("marshal video task payload: %w", err)
	}
	videoTask := asynq.NewTask(TypeGenerateVideo, payload)
	if _, err := p.Client.EnqueueContext(ctx, videoTask); err != nil {
		return fmt.Errorf("enqueue video task: %w", err)
	}
	return nil
}
