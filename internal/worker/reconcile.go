/*
 * internal/worker/reconcile.go
 *
 * Implements the HandleReconcileJobsTask background worker.
 * Sweeps the database for orphaned or stuck jobs in intermediate states
 * (PROCESSING, IMAGE_READY, VIDEO_READY) older than 1 hour and flags them as FAILED
 * so standard retry queues can self-heal and re-execute them.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

/*
 * HandleReconcileJobsTask sweeps the DB for jobs stuck in processing or intermediate
 * states for over 1 hour, shifting them to FAILED so they can trigger self-healing retries.
 */
func (p *VideoProcessor) HandleReconcileJobsTask(ctx context.Context, t *asynq.Task) error {
	slog.Info("starting background job reconciliation sweep")

	/* Cutoff is 1 hour ago */
	cutoff := time.Now().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")

	/* 1. Sweep jobs stuck in PROCESSING */
	stuckProcessingRows, err := p.DB.QueryContext(ctx,
		`SELECT id FROM video_jobs WHERE status = 'PROCESSING' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("query stuck processing jobs: %w", err)
	}
	defer stuckProcessingRows.Close()

	var processingReconciled int
	for stuckProcessingRows.Next() {
		var id string
		if err := stuckProcessingRows.Scan(&id); err != nil {
			slog.Error("failed to scan stuck processing row", "error", err)
			continue
		}

		/* Mark as FAILED so that the retry_count is incremented and requeued */
		errMsg := "stuck in PROCESSING for >1h; automatically reconciled to FAILED for retry"
		if err := db.SetJobFailed(p.DB, id, errMsg); err != nil {
			slog.Error("failed to reconcile stuck processing job to FAILED", "job_id", id, "error", err)
		} else {
			processingReconciled++
			slog.Warn("reconciled stuck processing job to FAILED", "job_id", id)
		}
	}

	/* 2. Sweep jobs stuck in intermediate local pipeline states (IMAGE_READY, VIDEO_READY) */
	stuckIntermediateRows, err := p.DB.QueryContext(ctx,
		`SELECT id, status FROM video_jobs WHERE status IN ('IMAGE_READY', 'VIDEO_READY') AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("query stuck intermediate jobs: %w", err)
	}
	defer stuckIntermediateRows.Close()

	var intermediateReconciled int
	for stuckIntermediateRows.Next() {
		var id, status string
		if err := stuckIntermediateRows.Scan(&id, &status); err != nil {
			slog.Error("failed to scan stuck intermediate row", "error", err)
			continue
		}

		errMsg := fmt.Sprintf("stuck in intermediate state %s for >1h; automatically reconciled to FAILED for retry", status)
		if err := db.SetJobFailed(p.DB, id, errMsg); err != nil {
			slog.Error("failed to reconcile stuck intermediate job to FAILED", "job_id", id, "status", status, "error", err)
		} else {
			intermediateReconciled++
			slog.Warn("reconciled stuck intermediate job to FAILED", "job_id", id, "status", status)
		}
	}

	slog.Info("background job reconciliation sweep completed",
		"processing_reconciled", processingReconciled,
		"intermediate_reconciled", intermediateReconciled,
	)
	return nil
}
