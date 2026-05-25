/*
 * internal/worker/cleanup_test.go
 *
 * Unit tests for the HandleDiskCleanupTask worker.
 * Verifies that stale files (completed, published, or failed older than 7 days)
 * are successfully pruned, while recent files or files with invalid cloud storage
 * urls are strictly preserved.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	_ "github.com/mattn/go-sqlite3"
)

func TestHandleDiskCleanupTask(t *testing.T) {
	/* Initialize a temporary in-memory database */
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE video_jobs (
			id TEXT PRIMARY KEY,
			status TEXT,
			cloud_storage_url TEXT,
			updated_at TEXT,
			completed_at TEXT
		)
	`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	/* Create temporary test directories */
	tempDir, err := os.MkdirTemp("", "bap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	defer os.Chdir(oldCwd)

	_ = os.MkdirAll(filepath.Join("data", "images"), 0755)
	_ = os.MkdirAll(filepath.Join("data", "audio"), 0755)
	_ = os.MkdirAll(filepath.Join("data", "videos"), 0755)

	staleTime := time.Now().AddDate(0, 0, -10).Format("2006-01-02 15:04:05")
	recentTime := time.Now().AddDate(0, 0, -2).Format("2006-01-02 15:04:05")

	/* Insert test data covering all logic branches */
	jobs := []struct {
		id       string
		status   string
		url      string
		compAt   string
		shouldRm bool
	}{
		/* Stale + valid S3 upload = should delete local assets */
		{"stale-comp-ok", "COMPLETED", "https://mybucket.s3.amazonaws.com/stale-comp-ok.mp4", staleTime, true},
		/* Stale + local fallback (stub) = should NOT delete local assets for safety */
		{"stale-comp-no-url", "COMPLETED", "local://stale-comp-no-url.mp4", staleTime, false},
		/* Stale + FAILED (no upload URL needed) = should delete local temp files */
		{"stale-failed", "FAILED", "", staleTime, true},
		/* Recent + valid S3 upload = should NOT delete (within 7-day retention) */
		{"recent-comp-ok", "COMPLETED", "https://mybucket.s3.amazonaws.com/recent-comp-ok.mp4", recentTime, false},
		/* Stale + PENDING = should NOT delete */
		{"stale-pending", "PENDING", "", staleTime, false},
	}

	for _, j := range jobs {
		_, err := db.Exec(
			"INSERT INTO video_jobs (id, status, cloud_storage_url, updated_at, completed_at) VALUES (?, ?, ?, ?, ?)",
			j.id, j.status, j.url, j.compAt, j.compAt,
		)
		if err != nil {
			t.Fatalf("failed to insert job %s: %v", j.id, err)
		}

		/* Create dummy files on disk to trace pruning */
		_ = os.WriteFile(filepath.Join("data", "videos", fmt.Sprintf("%s.mp4", j.id)), []byte("mp4"), 0644)
		_ = os.WriteFile(filepath.Join("data", "images", fmt.Sprintf("%s.png", j.id)), []byte("png"), 0644)
		_ = os.WriteFile(filepath.Join("data", "audio", fmt.Sprintf("%s.mp3", j.id)), []byte("mp3"), 0644)
	}

	/* Initialize the processor with output dir pointing to temp path */
	p := &VideoProcessor{
		DB:             db,
		VideoOutputDir: filepath.Join("data", "videos"),
	}

	/* Execute the disk cleanup task */
	ctx := context.Background()
	task := asynq.NewTask(TypeDiskCleanup, nil)
	if err := p.HandleDiskCleanupTask(ctx, task); err != nil {
		t.Fatalf("unexpected error running cleanup task: %v", err)
	}

	/* Verify on-disk presence for each file path */
	for _, j := range jobs {
		videoFile := filepath.Join("data", "videos", fmt.Sprintf("%s.mp4", j.id))
		_, statErr := os.Stat(videoFile)
		exists := statErr == nil

		if j.shouldRm && exists {
			t.Errorf("expected file %s to be removed, but it still exists", videoFile)
		}
		if !j.shouldRm && !exists {
			t.Errorf("expected file %s to remain, but it was deleted", videoFile)
		}
	}
}
