/*
 * internal/db/jobs_test.go
 *
 * Tests for video job CRUD operations and UUID generation.
 * Uses an in-memory SQLite database to avoid filesystem dependencies.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package db

import (
	"database/sql"
	"regexp"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

/* setupTestDB creates an in-memory SQLite DB and runs migrations. */
func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}

	/* Run the schema migrations (from sqlite.go) */
	if err := migrate(db); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	return db
}

/* seedUnusedPrompt inserts a test prompt and returns its ID. */
func seedUnusedPrompt(t *testing.T, db *sql.DB, text string) int64 {
	res, err := db.Exec(
		`INSERT INTO prompts (seed_text, enriched_text, status, builder_used)
		 VALUES (?, ?, 'UNUSED', 'TEST')`,
		"seed "+text, text,
	)
	if err != nil {
		t.Fatalf("failed to seed prompt: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

/* --- UUID Tests --- */

func TestGenerateUUID_Format(t *testing.T) {
	uuid, err := generateUUID()
	if err != nil {
		t.Fatalf("generateUUID failed: %v", err)
	}

	/* Verify format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx */
	/* y must be 8, 9, a, or b (RFC 4122 variant) */
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(uuid) {
		t.Errorf("UUID %q does not match expected format", uuid)
	}
}

func TestGenerateUUID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		uuid, err := generateUUID()
		if err != nil {
			t.Fatalf("generateUUID failed at iteration %d: %v", i, err)
		}
		if seen[uuid] {
			t.Fatalf("duplicate UUID generated: %s", uuid)
		}
		seen[uuid] = true
	}
}

/* --- CRUD Tests --- */

func TestCreateJob(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	/* Seed a prompt */
	promptID := seedUnusedPrompt(t, db, "test prompt 1")

	/* Create the job */
	job, err := CreateJob(db, "RUNWAY")
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	/* Assert job properties */
	if job.ID == "" {
		t.Error("expected job ID to be populated")
	}
	if job.PromptID != promptID {
		t.Errorf("expected PromptID %d, got %d", promptID, job.PromptID)
	}
	if job.AIProvider != "RUNWAY" {
		t.Errorf("expected AIProvider 'RUNWAY', got %q", job.AIProvider)
	}
	if job.Status != "PENDING" {
		t.Errorf("expected Status 'PENDING', got %q", job.Status)
	}
	if job.PromptTextSnapshot != "test prompt 1" {
		t.Errorf("expected snapshot 'test prompt 1', got %q", job.PromptTextSnapshot)
	}

	/* Assert prompt was marked USED */
	var promptStatus string
	if err := db.QueryRow("SELECT status FROM prompts WHERE id = ?", promptID).Scan(&promptStatus); err != nil {
		t.Fatalf("failed to query prompt status: %v", err)
	}
	if promptStatus != "USED" {
		t.Errorf("expected prompt status 'USED', got %q", promptStatus)
	}
}

func TestCreateJob_NoUnusedPrompts(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	/* Do not seed any prompts */
	job, err := CreateJob(db, "RUNWAY")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got: %v", err)
	}
	if job != nil {
		t.Error("expected job to be nil")
	}
}

func TestCreateJob_Sequential(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "prompt 1")
	seedUnusedPrompt(t, db, "prompt 2")

	job1, err := CreateJob(db, "RUNWAY")
	if err != nil {
		t.Fatalf("first CreateJob failed: %v", err)
	}

	job2, err := CreateJob(db, "RUNWAY")
	if err != nil {
		t.Fatalf("second CreateJob failed: %v", err)
	}

	if job1.ID == job2.ID {
		t.Error("expected different job IDs")
	}
	if job1.PromptID == job2.PromptID {
		t.Error("expected different prompt IDs")
	}
	if job1.PromptTextSnapshot == job2.PromptTextSnapshot {
		t.Error("expected different snapshots")
	}

	/* Third call should fail with no rows */
	_, err = CreateJob(db, "RUNWAY")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows for third call, got: %v", err)
	}
}

func TestJobLifecycle(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "lifecycle test")
	job, _ := CreateJob(db, "RUNWAY")

	/* 1. Mark PROCESSING */
	if err := SetJobProcessing(db, job.ID, "task_123"); err != nil {
		t.Fatalf("SetJobProcessing failed: %v", err)
	}

	/* Fetch processing jobs */
	procJobs, err := GetProcessingJobs(db)
	if err != nil {
		t.Fatalf("GetProcessingJobs failed: %v", err)
	}
	if len(procJobs) != 1 {
		t.Fatalf("expected 1 processing job, got %d", len(procJobs))
	}
	if procJobs[0].AITaskID != "task_123" {
		t.Errorf("expected AITaskID 'task_123', got %q", procJobs[0].AITaskID)
	}

	/* 2. Mark COMPLETED */
	if err := SetJobCompleted(db, job.ID, "https://example.com/video.mp4"); err != nil {
		t.Fatalf("SetJobCompleted failed: %v", err)
	}

	/* Verify status update in DB */
	var status, url string
	if err := db.QueryRow("SELECT status, cloud_storage_url FROM video_jobs WHERE id = ?", job.ID).Scan(&status, &url); err != nil {
		t.Fatalf("query job failed: %v", err)
	}
	if status != "COMPLETED" {
		t.Errorf("expected status 'COMPLETED', got %q", status)
	}
	if url != "https://example.com/video.mp4" {
		t.Errorf("expected url 'https://example.com/video.mp4', got %q", url)
	}
}

func TestSetJobFailed(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "failure test")
	job, _ := CreateJob(db, "RUNWAY")

	if err := SetJobFailed(db, job.ID, "API timeout"); err != nil {
		t.Fatalf("SetJobFailed failed: %v", err)
	}

	var status, errorLog string
	var retryCount int
	if err := db.QueryRow("SELECT status, error_log, retry_count FROM video_jobs WHERE id = ?", job.ID).Scan(&status, &errorLog, &retryCount); err != nil {
		t.Fatalf("query job failed: %v", err)
	}

	if status != "FAILED" {
		t.Errorf("expected status 'FAILED', got %q", status)
	}
	if errorLog != "API timeout" {
		t.Errorf("expected error log 'API timeout', got %q", errorLog)
	}
	if retryCount != 1 {
		t.Errorf("expected retry count 1, got %d", retryCount)
	}
}

func TestGetPendingJobs(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "1")
	seedUnusedPrompt(t, db, "2")
	seedUnusedPrompt(t, db, "3")

	CreateJob(db, "RUNWAY")
	CreateJob(db, "RUNWAY")
	CreateJob(db, "RUNWAY")

	/* Get 2 pending jobs */
	jobs, err := GetPendingJobs(db, 2)
	if err != nil {
		t.Fatalf("GetPendingJobs failed: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 pending jobs, got %d", len(jobs))
	}
	if jobs[0].Status != "PENDING" || jobs[1].Status != "PENDING" {
		t.Error("expected jobs to have PENDING status")
	}
}
