/*
 * internal/worker/generate.go
 *
 * Implements the handler for the video:generate task.
 * Atomically picks up an unused prompt and sends it to the AI video provider.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/db"
	"hereticrush/bap/internal/publisher"
	"github.com/hibiken/asynq"
)

/*
 * VideoProcessor encapsulates the dependencies needed by the
 * worker handlers to process jobs.
 */
type VideoProcessor struct {
	DB             *sql.DB
	Provider       video.AIVideoProvider
	Publisher      publisher.Publisher
	Client         *asynq.Client
	VideoOutputDir string
}

/*
 * HandleGenerateVideoTask claims a pending job atomically from the DB,
 * submits it to the video provider, and marks it as PROCESSING.
 */
func (p *VideoProcessor) HandleGenerateVideoTask(ctx context.Context, t *asynq.Task) error {
	/* 1. Atomically create a job from an unused prompt */
	job, err := db.CreateJob(p.DB, p.Provider.Name())
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Debug("no unused prompts available, task skipping")
			return nil /* Graceful exit, will be tried again by scheduler */
		}
		return fmt.Errorf("create job: %w", err)
	}

	slog.Info("starting video generation", "job_id", job.ID, "provider", p.Provider.Name())

	/* 2. Build the provider request */
	req := video.GenerationRequest{
		Prompt:      job.PromptTextSnapshot,
		Duration:    5,
		AspectRatio: "1280:720",
	}

	/* 3. Submit to AI Provider */
	taskID, err := p.Provider.GenerateVideo(ctx, req)
	if err != nil {
		/* Mark as failed so it can be retried */
		if setErr := db.SetJobFailed(p.DB, job.ID, err.Error()); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", job.ID, "error", setErr)
		}
		return fmt.Errorf("generate video: %w", err)
	}

	/* 4. Update job to PROCESSING */
	if err := db.SetJobProcessing(p.DB, job.ID, taskID); err != nil {
		return fmt.Errorf("set job processing: %w", err)
	}

	slog.Info("video generation submitted successfully", "job_id", job.ID, "ai_task_id", taskID)
	return nil
}
