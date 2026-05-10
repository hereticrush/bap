/*
 * internal/worker/worker_test.go
 *
 * Unit tests for worker handlers using an in-memory miniredis server.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"context"
	"database/sql"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hibiken/asynq"
	_ "github.com/mattn/go-sqlite3"
)

/* Mock provider for testing */
type mockVideoProvider struct {
	taskIDToReturn string
	statusToReturn video.GenerationResult
	generateCalled bool
	checkCalled    bool
}

func (m *mockVideoProvider) Name() string { return "MOCK" }
func (m *mockVideoProvider) GenerateVideo(ctx context.Context, req video.GenerationRequest) (string, error) {
	m.generateCalled = true
	return m.taskIDToReturn, nil
}
func (m *mockVideoProvider) CheckStatus(ctx context.Context, taskID string) (video.GenerationResult, error) {
	m.checkCalled = true
	return m.statusToReturn, nil
}

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	
	/* simplified migration just for tests */
	_, err = db.Exec(`
		CREATE TABLE prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			seed_text TEXT,
			enriched_text TEXT,
			status TEXT,
			tokens_used INTEGER,
			builder_used TEXT
		);
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
			youtube_video_id TEXT,
			error_log TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	return db
}

func TestHandleGenerateVideoTask(t *testing.T) {
	/* 1. Start miniredis */
	mr := miniredis.RunT(t)
	
	/* 2. Setup isolated DB */
	db := setupTestDB(t)
	defer db.Close()
	
	/* Seed an unused prompt */
	db.Exec("INSERT INTO prompts (seed_text, enriched_text, status, builder_used) VALUES ('a', 'b', 'UNUSED', 'TEST')")

	/* 3. Setup Asynq client connecting to miniredis */
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	provider := &mockVideoProvider{taskIDToReturn: "task_123"}
	
	processor := &VideoProcessor{
		DB:       db,
		Provider: provider,
		Client:   client,
	}

	/* 4. Execute the handler directly */
	task := asynq.NewTask(TypeGenerateVideo, nil)
	err := processor.HandleGenerateVideoTask(context.Background(), task)
	if err != nil {
		t.Fatalf("HandleGenerateVideoTask failed: %v", err)
	}

	/* 5. Verify the provider was called */
	if !provider.generateCalled {
		t.Error("expected GenerateVideo to be called")
	}

	/* 6. Verify the DB state advanced to PROCESSING */
	var status string
	db.QueryRow("SELECT status FROM video_jobs LIMIT 1").Scan(&status)
	if status != "PROCESSING" {
		t.Errorf("expected job status PROCESSING, got %q", status)
	}
}
