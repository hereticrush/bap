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
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/hereticrush/bap/internal/db"
	"github.com/hereticrush/bap/internal/publisher"

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

	/* 3. Retrieve the job to get the prompt metadata for Title/Description/Tags/Playlist */
	var promptText string
	var metadataJSON sql.NullString
	err := p.DB.QueryRow(
		"SELECT prompt_text_snapshot, metadata FROM video_jobs WHERE id = ?", jobID,
	).Scan(&promptText, &metadataJSON)
	if err != nil {
		return fmt.Errorf("fetch job metadata: %w", err)
	}

	meta := db.ParseJobMetadata(metadataJSON.String)

	/* Extract custom, LLM-generated, or fallback parameters */
	defaultTitle := "AI Generated Video"
	if len(promptText) > 50 {
		defaultTitle = promptText[:50] + "..."
	}
	title := meta.GetYoutubeTitle(defaultTitle)
	description := meta.GetYoutubeDescription(promptText)
	privacy := meta.GetYoutubePrivacy("private")
	tags := meta.GetYoutubeTags([]string{"ai", "generated", "bap"})
	playlistID := meta.GetYoutubePlaylistID()

	/* Automatically locate local image anchor for custom video thumbnail */
	thumbnailPath := ""
	if anchors := meta.GetImageAnchorsLocal(); len(anchors) > 0 {
		thumbnailPath = anchors[0]
	}

	req := publisher.PublishRequest{
		FilePath:      videoPath,
		Title:         title,
		Description:   description,
		Tags:          tags,
		Privacy:       privacy,
		ThumbnailPath: thumbnailPath,
		PlaylistID:    playlistID,
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
