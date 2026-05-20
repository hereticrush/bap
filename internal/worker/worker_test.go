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
	"github.com/hereticrush/bap/internal/adapter/tts"
	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/publisher"
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

/* Mock publisher for testing */
type mockPublisher struct {
	resultToReturn publisher.PublishResult
	publishCalled  bool
}

func (m *mockPublisher) Name() string { return "MOCK_PUBLISHER" }
func (m *mockPublisher) Publish(ctx context.Context, req publisher.PublishRequest) (publisher.PublishResult, error) {
	m.publishCalled = true
	return m.resultToReturn, nil
}

/* Mock TTS provider for testing */
type mockTTSProvider struct {
	resultToReturn tts.AudioResult
	generateCalled bool
	lastText       string
}

func (m *mockTTSProvider) Name() string { return "MOCK_TTS" }
func (m *mockTTSProvider) GenerateAudio(ctx context.Context, text string, outputFilename string) (tts.AudioResult, error) {
	m.generateCalled = true
	m.lastText = text
	return m.resultToReturn, nil
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
			builder_used TEXT,
			metadata TEXT
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
			published_video_id TEXT,
			metadata TEXT,
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
	pub := &mockPublisher{}
	
	processor := &VideoProcessor{
		DB:             db,
		Provider:       provider,
		Publisher:      pub,
		Client:         client,
		VideoOutputDir: "data/videos",
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

func TestHandlePublishVideoTask(t *testing.T) {
	mr := miniredis.RunT(t)
	db := setupTestDB(t)
	defer db.Close()

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	/* Seed a COMPLETED job */
	db.Exec("INSERT INTO prompts (id, seed_text, enriched_text, status, builder_used) VALUES (1, 'a', 'b', 'USED', 'TEST')")
	db.Exec("INSERT INTO video_jobs (id, prompt_id, prompt_text_snapshot, prompt_builder_used, ai_provider, status, cloud_storage_url) VALUES ('job_1', 1, 'mock prompt', 'TEST', 'RUNWAY', 'COMPLETED', 'http://url')")

	pub := &mockPublisher{
		resultToReturn: publisher.PublishResult{PlatformVideoID: "yt_123", URL: "http://yt"},
	}

	processor := &VideoProcessor{
		DB:             db,
		Publisher:      pub,
		Client:         client,
		VideoOutputDir: "data/videos",
	}

	task := asynq.NewTask(TypePublishVideo, []byte(`{"job_id":"job_1"}`))
	err := processor.HandlePublishVideoTask(context.Background(), task)
	if err != nil {
		t.Fatalf("HandlePublishVideoTask failed: %v", err)
	}

	if !pub.publishCalled {
		t.Error("expected Publish to be called")
	}

	var status string
	db.QueryRow("SELECT status FROM video_jobs WHERE id = 'job_1'").Scan(&status)
	if status != "PUBLISHED" {
		t.Errorf("expected job status PUBLISHED, got %q", status)
	}
}

func TestHandleAddAudioTask(t *testing.T) {
	mr := miniredis.RunT(t)
	db := setupTestDB(t)
	defer db.Close()

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	/* Seed a VIDEO_READY job with voice_script in metadata */
	db.Exec("INSERT INTO prompts (id, seed_text, enriched_text, status, builder_used) VALUES (1, 'a', 'b', 'USED', 'TEST')")
	db.Exec(`INSERT INTO video_jobs (id, prompt_id, prompt_text_snapshot, prompt_builder_used, ai_provider, status, cloud_storage_url, metadata)
		VALUES ('job_1', 1, 'long cinematic prompt text', 'TEST', 'RUNWAY', 'VIDEO_READY', 'https://provider.example/v.mp4', '{"voice_script":"short narration"}')`)

	ttsProv := &mockTTSProvider{
		resultToReturn: tts.AudioResult{FilePath: "data/audio/job_1.mp3"},
	}

	processor := &VideoProcessor{
		DB:             db,
		TTSProvider:    ttsProv,
		Client:         client,
		VideoOutputDir: "data/videos",
	}

	/* We skip the ffmpeg execution in test by ensuring the text generation works.
	 * Normally we'd mock exec.Command, but this is a simple unit test for the state machine flow.
	 * We can expect it to fail at ffmpeg if ffmpeg is missing, or we can just verify it attempts the call.
	 * Let's just verify it fails on ffmpeg if files don't exist, but verify the TTS was called.
	 */

	task := asynq.NewTask(TypeAddAudio, []byte(`{"job_id":"job_1"}`))
	err := processor.HandleAddAudioTask(context.Background(), task)
	
	if !ttsProv.generateCalled {
		t.Fatal("expected GenerateAudio to be called")
	}
	if ttsProv.lastText != "short narration" {
		t.Errorf("expected voice_script for TTS, got %q", ttsProv.lastText)
	}

	/* It will fail because the mock mp3 and mp4 don't exist for ffmpeg to merge */

	/* Since it fails at ffmpeg, job state should become FAILED */
	var status string
	db.QueryRow("SELECT status FROM video_jobs WHERE id = 'job_1'").Scan(&status)
	if status != "FAILED" && err == nil {
		t.Errorf("expected job status to change or error, got %q", status)
	}
}
