/*
 * internal/worker/reconcile_test.go
 *
 * Unit tests for the HandleReconcileJobsTask background worker.
 * Verifies that stuck jobs in PROCESSING, IMAGE_READY, or VIDEO_READY states
 * older than 1 hour are successfully transitioned to FAILED with retry count increments,
 * while recent or successfully finalized jobs remain completely untouched.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	_ "github.com/mattn/go-sqlite3"
)

func TestHandleReconcileJobsTask(t *testing.T) {
	/* Initialize a temporary in-memory database */
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE video_jobs (
			id TEXT PRIMARY KEY,
			prompt_id INTEGER,
			prompt_text_snapshot TEXT,
			prompt_builder_used TEXT,
			ai_provider TEXT,
			status TEXT,
			retry_count INTEGER DEFAULT 0,
			ai_task_id TEXT,
			cloud_storage_url TEXT,
			published_video_id TEXT,
			metadata TEXT,
			error_log TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			completed_at TEXT
		)
	`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	staleTime := time.Now().Add(-90 * time.Minute).Format("2006-01-02 15:04:05")
	recentTime := time.Now().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")

	/* Insert test jobs covering all reconciliation branches */
	testJobs := []struct {
		id            string
		status        string
		updatedAt     string
		shouldFail    bool
		expectedRetry int
	}{
		/* Processing, older than 1h -> should be set to FAILED, retry_count incremented */
		{"job-stale-proc", "PROCESSING", staleTime, true, 1},
		/* Processing, recent -> should NOT be modified */
		{"job-recent-proc", "PROCESSING", recentTime, false, 0},
		/* Image Ready, older than 1h -> should be set to FAILED, retry_count incremented */
		{"job-stale-img", "IMAGE_READY", staleTime, true, 1},
		/* Video Ready, older than 1h -> should be set to FAILED, retry_count incremented */
		{"job-stale-vid", "VIDEO_READY", staleTime, true, 1},
		/* Completed/Published, older than 1h -> should NOT be modified */
		{"job-stale-done", "COMPLETED", staleTime, false, 0},
		{"job-stale-pub", "PUBLISHED", staleTime, false, 0},
	}

	for _, j := range testJobs {
		_, err := db.Exec(
			`INSERT INTO video_jobs (id, prompt_text_snapshot, ai_provider, status, retry_count, updated_at)
			 VALUES (?, 'prompt text', 'MOCK', ?, 0, ?)`,
			j.id, j.status, j.updatedAt,
		)
		if err != nil {
			t.Fatalf("failed to insert test job %s: %v", j.id, err)
		}
	}

	processor := &VideoProcessor{
		DB: db,
	}

	ctx := context.Background()
	task := asynq.NewTask(TypeReconcileJobs, nil)
	if err := processor.HandleReconcileJobsTask(ctx, task); err != nil {
		t.Fatalf("unexpected error running reconciliation task: %v", err)
	}

	/* Verify results in the database */
	for _, j := range testJobs {
		var status, errLog sql.NullString
		var retryCount int
		err := db.QueryRow("SELECT status, retry_count, error_log FROM video_jobs WHERE id = ?", j.id).
			Scan(&status, &retryCount, &errLog)
		if err != nil {
			t.Fatalf("failed to fetch status for job %s: %v", j.id, err)
		}

		if j.shouldFail {
			if !status.Valid || status.String != "FAILED" {
				t.Errorf("expected job %s to be FAILED, got %s", j.id, status.String)
			}
			if retryCount != j.expectedRetry {
				t.Errorf("expected job %s to have retry count %d, got %d", j.id, j.expectedRetry, retryCount)
			}
			if !errLog.Valid || errLog.String == "" {
				t.Errorf("expected error log to be recorded for reconciled job %s, got empty", j.id)
			}
		} else {
			if !status.Valid || status.String != j.status {
				t.Errorf("expected job %s status to remain %s, got %s", j.id, j.status, status.String)
			}
			if retryCount != j.expectedRetry {
				t.Errorf("expected job %s retry count to remain %d, got %d", j.id, j.expectedRetry, retryCount)
			}
		}
	}
}
