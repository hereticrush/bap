/*
 * internal/worker/poll.go
 *
 * Implements the handler for the video:poll task.
 * Iterates through all processing jobs and checks their status.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

/*
 * HandlePollStatusTask fetches all jobs currently in PROCESSING
 * status and checks their completion status with the AI provider.
 */
func (p *VideoProcessor) HandlePollStatusTask(ctx context.Context, t *asynq.Task) error {
	jobs, err := db.GetProcessingJobs(p.DB)
	if err != nil {
		return fmt.Errorf("get processing jobs: %w", err)
	}

	for _, job := range jobs {
		if job.AITaskID == "" {
			continue /* Sanity check, should not happen */
		}

		res, err := p.Provider.CheckStatus(ctx, job.AITaskID)
		if err != nil {
			slog.Error("check status failed", "job_id", job.ID, "error", err)
			continue /* Move to next job */
		}

		switch res.Status {
		case video.StatusCompleted:
			/* 1. Mark in DB */
			if err := db.SetJobCompleted(p.DB, job.ID, res.VideoURL); err != nil {
				slog.Error("set job completed failed", "job_id", job.ID, "error", err)
				continue
			}

			slog.Info("video generation completed", "job_id", job.ID, "video_url", res.VideoURL)

			/* 2. Enqueue the download task */
			payload, err := json.Marshal(DownloadPayload{
				JobID:    job.ID,
				VideoURL: res.VideoURL,
			})
			if err != nil {
				slog.Error("marshal download payload failed", "job_id", job.ID, "error", err)
				continue
			}

			dlTask := asynq.NewTask(TypeDownloadVideo, payload)
			if _, err := p.Client.EnqueueContext(ctx, dlTask); err != nil {
				slog.Error("enqueue download task failed", "job_id", job.ID, "error", err)
			}

		case video.StatusFailed:
			/* Mark failed so it can be manually reviewed or automatically retried */
			if err := db.SetJobFailed(p.DB, job.ID, res.Error); err != nil {
				slog.Error("set job failed status error", "job_id", job.ID, "error", err)
			}
			slog.Info("video generation failed", "job_id", job.ID, "error", res.Error)

		case video.StatusPending, video.StatusProcessing:
			/* Still working, do nothing. */
			slog.Debug("job still processing", "job_id", job.ID)
		}
	}

	return nil
}
