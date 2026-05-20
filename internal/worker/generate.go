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
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hereticrush/bap/internal/adapter/image"
	"github.com/hereticrush/bap/internal/adapter/tts"
	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hereticrush/bap/internal/publisher"
	"github.com/hibiken/asynq"
)

/*
 * VideoProcessor encapsulates the dependencies needed by the
 * worker handlers to process jobs.
 */
type VideoProcessor struct {
	DB                  *sql.DB
	Provider            video.AIVideoProvider
	Publisher           publisher.Publisher
	TTSProvider         tts.TTSProvider
	ImageProvider       image.AIImageProvider
	Uploader            video.AssetUploader
	DefaultImageAnchors bool
	Client              *asynq.Client
	VideoOutputDir      string
}

/*
 * HandleGenerateVideoTask claims a pending job atomically from the DB,
 * submits it to the video provider, and marks it as PROCESSING.
 */
func (p *VideoProcessor) HandleGenerateVideoTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	jobID := payload.JobID
	slog.Info("starting video generation", "job_id", jobID, "provider", p.Provider.Name())

	/* 1. Fetch the job from the DB */
	var promptText string
	var metadataJSON sql.NullString
	err := p.DB.QueryRow(
		"SELECT prompt_text_snapshot, metadata FROM video_jobs WHERE id = ?", jobID,
	).Scan(&promptText, &metadataJSON)
	if err != nil {
		return fmt.Errorf("fetch job: %w", err)
	}

	/* 2. Build the provider request */
	req := video.GenerationRequest{
		Prompt:      promptText,
		Duration:    5,
		AspectRatio: "1280:720",
	}

	if metadataJSON.Valid && metadataJSON.String != "" {
		var meta struct {
			ImageAnchors []string `json:"image_anchors"`
		}
		if err := json.Unmarshal([]byte(metadataJSON.String), &meta); err == nil {
			for _, ref := range meta.ImageAnchors {
				if ref == "" {
					continue
				}
				if !db.IsProviderImageRef(ref) {
					if setErr := db.SetJobFailed(p.DB, jobID,
						fmt.Sprintf("image_anchors contains local path %q; re-run pipeline after deploy", ref)); setErr != nil {
						slog.Error("failed to set job failed status", "job_id", jobID, "error", setErr)
					}
					return fmt.Errorf("invalid image anchor reference: %s", ref)
				}
				req.ImageURLs = append(req.ImageURLs, ref)
			}
		} else {
			slog.Warn("failed to parse job metadata", "job_id", jobID, "error", err)
		}
	}

	if len(req.ImageURLs) > 0 {
		slog.Info("submitting with image anchor", "job_id", jobID, "anchor", req.ImageURLs[0])
	} else if strings.Contains(metadataJSON.String, db.MetadataKeyImageAnchors) {
		slog.Warn("image_anchors key present but no valid provider refs", "job_id", jobID)
	}
	/* 3. Submit to AI Provider */
	taskID, err := p.Provider.GenerateVideo(ctx, req)
	if err != nil {
		/* Mark as failed so it can be retried */
		if setErr := db.SetJobFailed(p.DB, jobID, err.Error()); setErr != nil {
			slog.Error("failed to set job failed status", "job_id", jobID, "error", setErr)
		}
		return fmt.Errorf("generate video: %w", err)
	}

	/* 4. Update job to PROCESSING */
	if err := db.SetJobProcessing(p.DB, jobID, taskID); err != nil {
		return fmt.Errorf("set job processing: %w", err)
	}

	slog.Info("video generation submitted successfully", "job_id", jobID, "ai_task_id", taskID)
	return nil
}
