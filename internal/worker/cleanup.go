/*
 * internal/worker/cleanup.go
 *
 * Implements the HandleDiskCleanupTask background worker.
 * Sweeps the database for completed, published, or failed jobs older than
 * 7 days and purges their local temporary video, audio, and image assets.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

/*
 * HandleDiskCleanupTask sweeps local temp directories to purge old assets.
 * Only deletes local files if the cloud storage upload is verified (for successful jobs).
 */
func (p *VideoProcessor) HandleDiskCleanupTask(ctx context.Context, t *asynq.Task) error {
	slog.Info("starting scheduled disk cleanup task")

	/* Cutoff is 7 days ago */
	cutoff := time.Now().AddDate(0, 0, -7).Format("2006-01-02 15:04:05")

	rows, err := p.DB.QueryContext(ctx,
		`SELECT id, status, cloud_storage_url FROM video_jobs
		 WHERE (status IN ('COMPLETED', 'PUBLISHED', 'FAILED'))
		   AND (completed_at < ? OR (completed_at IS NULL AND updated_at < ?))`,
		cutoff, cutoff,
	)
	if err != nil {
		return fmt.Errorf("query stale jobs: %w", err)
	}
	defer rows.Close()

	var filesDeleted int
	var bytesFreed int64

	for rows.Next() {
		var id, status string
		var cloudURL string
		if err := rows.Scan(&id, &status, &cloudURL); err != nil {
			slog.Error("failed to scan stale job row", "error", err)
			continue
		}

		/*
		 * Safety check: For successful jobs, ensure the asset has been uploaded
		 * and mapped to a valid cloud storage URL. Local storage URLs (local://)
		 * are not valid cloud offloads.
		 */
		if (status == "COMPLETED" || status == "PUBLISHED") && (cloudURL == "" || strings.HasPrefix(cloudURL, "local://")) {
			slog.Warn("skipping asset cleanup for job: no valid cloud storage offload present", "job_id", id, "url", cloudURL)
			continue
		}

		/* Prune local temp files: mp4 (video), png (image), mp3 (audio) */
		localVideoPath := filepath.Join(p.VideoOutputDir, fmt.Sprintf("%s.mp4", id))
		localImagePath := filepath.Join("data", "images", fmt.Sprintf("%s.png", id))
		localAudioPath := filepath.Join("data", "audio", fmt.Sprintf("%s.mp3", id))

		pathsToPrune := []string{localVideoPath, localImagePath, localAudioPath}

		for _, path := range pathsToPrune {
			if info, err := os.Stat(path); err == nil {
				size := info.Size()
				if rmErr := os.Remove(path); rmErr == nil {
					filesDeleted++
					bytesFreed += size
					slog.Debug("pruned stale local asset", "path", path, "bytes", size)
				} else {
					slog.Error("failed to delete local asset", "path", path, "error", rmErr)
				}
			}
		}
	}

	slog.Info("disk cleanup task completed",
		"files_deleted", filesDeleted,
		"megabytes_freed", fmt.Sprintf("%.2f", float64(bytesFreed)/(1024*1024)),
	)
	return nil
}
