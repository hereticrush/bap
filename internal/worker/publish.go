/*
 * internal/worker/publish.go
 *
 * Implements the Asynq task handler for publishing a finished video.
 * It uses the generic publisher.Publisher interface so it remains
 * agnostic to the specific platform (YouTube, TikTok, etc.).
 */
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"hereticrush/bap/internal/db"
	"hereticrush/bap/internal/publisher"

	"github.com/hibiken/asynq"
)

/*
 * HandlePublishVideoTask executes the platform-agnostic publish flow.
 */
func (p *VideoProcessor) HandlePublishVideoTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %w", err)
	}

	jobID := payload.JobID

	/* 1. Ensure the publisher is available */
	if p.Publisher == nil {
		err := fmt.Errorf("no publisher configured for worker")
		slog.Error("publish task failed", "job_id", jobID, "error", err)
		return err
	}

	/* 2. Determine the path to the downloaded video */
	videoPath := filepath.Join(p.VideoOutputDir, fmt.Sprintf("%s.mp4", jobID))

	/* 3. Retrieve the job to get the prompt metadata for Title/Description */
	// We don't have a GetJobByID yet, let's just query it directly or add it.
	// We will query it directly here to avoid adding more files right now.
	var promptText string
	err := p.DB.QueryRow(
		"SELECT prompt_text_snapshot FROM video_jobs WHERE id = ?", jobID,
	).Scan(&promptText)
	if err != nil {
		return fmt.Errorf("fetch job metadata: %w", err)
	}

	/* Extract title (first line or first 50 chars) and description */
	title := "AI Generated Video"
	description := promptText
	if len(promptText) > 50 {
		title = promptText[:50] + "..."
	}

	req := publisher.PublishRequest{
		FilePath:    videoPath,
		Title:       title,
		Description: description,
		Tags:        []string{"ai", "generated", "bap"},
		Privacy:     "private",
	}

	slog.Info("publishing video", "job_id", jobID, "platform", p.Publisher.Name(), "file", videoPath)

	/* 4. Call the publisher adapter */
	res, err := p.Publisher.Publish(ctx, req)
	if err != nil {
		/* We don't mark the job as FAILED here; asynq will retry.
		 * If it exhausts retries, it will move to the archive/dead queue. */
		slog.Error("publisher adapter failed", "job_id", jobID, "error", err)
		return fmt.Errorf("publisher failed: %w", err)
	}

	/* 5. Update the database on success */
	if err := db.SetJobPublished(p.DB, jobID, res.PlatformVideoID); err != nil {
		slog.Error("failed to mark job as published in DB", "job_id", jobID, "error", err)
		return fmt.Errorf("db update failed: %w", err)
	}

	slog.Info("video published successfully",
		"job_id", jobID,
		"platform", p.Publisher.Name(),
		"url", res.URL,
	)

	return nil
}
