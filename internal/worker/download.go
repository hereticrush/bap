/*
 * internal/worker/download.go
 *
 * Implements the handler for the video:download task.
 * Downloads the finished MP4 from the AI provider to local disk.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

/*
 * DownloadPayload contains the data passed from the poller
 * to the downloader task.
 */
type DownloadPayload struct {
	JobID    string `json:"job_id"`
	VideoURL string `json:"video_url"`
}

/*
 * HandleDownloadVideoTask parses the cloud storage URL and
 * streams the video to the local data/videos folder.
 */
func (p *VideoProcessor) HandleDownloadVideoTask(ctx context.Context, t *asynq.Task) error {
	var payload DownloadPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	slog.Info("downloading video", "job_id", payload.JobID)

	req, err := http.NewRequestWithContext(ctx, "GET", payload.VideoURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code downloading video: %s", resp.Status)
	}

	/* Ensure the directory exists */
	if err := os.MkdirAll("data/videos", 0755); err != nil {
		return fmt.Errorf("mkdir data/videos: %w", err)
	}

	filePath := filepath.Join("data/videos", fmt.Sprintf("%s.mp4", payload.JobID))
	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("copy stream: %w", err)
	}

	/* Update Database to VIDEO_READY and record local file path in metadata */
	if err := db.MergeJobMetadata(p.DB, payload.JobID, map[string]interface{}{
		db.MetadataKeyLocalVideoPath: filePath,
	}); err != nil {
		return fmt.Errorf("update job metadata: %w", err)
	}
	if err := db.SetJobVideoReady(p.DB, payload.JobID); err != nil {
		return fmt.Errorf("set job video ready: %w", err)
	}

	/* Enqueue Add Audio Task */
	audioTask := asynq.NewTask(TypeAddAudio, t.Payload())
	if _, err := p.Client.EnqueueContext(ctx, audioTask); err != nil {
		return fmt.Errorf("enqueue add audio task: %w", err)
	}

	slog.Info("video download complete, enqueued audio task", "job_id", payload.JobID, "file", filePath)
	return nil
}
