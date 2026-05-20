package worker

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hereticrush/bap/internal/adapter/image"
	"github.com/hereticrush/bap/internal/adapter/video"
	"github.com/hereticrush/bap/internal/db"
	"github.com/hibiken/asynq"
)

type mockImageProvider struct {
	called bool
}

func (m *mockImageProvider) Name() string { return "MOCK_IMAGE" }
func (m *mockImageProvider) GenerateImage(ctx context.Context, req image.GenerationRequest, outputFilename string) (image.GenerationResult, error) {
	m.called = true
	return image.GenerationResult{FilePath: outputFilename}, nil
}

type mockAssetUploader struct {
	uri    string
	called bool
}

func (m *mockAssetUploader) UploadImage(ctx context.Context, localPath string) (string, error) {
	m.called = true
	if m.uri == "" {
		return "runway://test/anchor", nil
	}
	return m.uri, nil
}

func TestHandleStartPipeline_TextOnly(t *testing.T) {
	mr := miniredis.RunT(t)
	database := setupTestDB(t)
	defer database.Close()

	_, err := database.Exec(
		`INSERT INTO prompts (seed_text, enriched_text, status, builder_used, metadata)
		 VALUES ('s', 'enriched', 'UNUSED', 'TEST', ?)`,
		`{"use_image_anchor":"false"}`,
	)
	if err != nil {
		t.Fatal(err)
	}

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	img := &mockImageProvider{}
	uploader := &mockAssetUploader{}
	processor := &VideoProcessor{
		DB:                  database,
		Provider:            &mockVideoProvider{taskIDToReturn: "t1"},
		Client:              client,
		ImageProvider:       img,
		Uploader:            uploader,
		DefaultImageAnchors: true,
		VideoOutputDir:      "data/videos",
	}

	if err := processor.HandleStartPipelineTask(context.Background(), asynq.NewTask(TypeStartPipeline, nil)); err != nil {
		t.Fatalf("HandleStartPipelineTask: %v", err)
	}
	if img.called {
		t.Error("expected no image generation when use_image_anchor=false")
	}
	if uploader.called {
		t.Error("expected no upload when use_image_anchor=false")
	}

	var status string
	database.QueryRow("SELECT status FROM video_jobs LIMIT 1").Scan(&status)
	if status != "PENDING" {
		t.Errorf("expected PENDING for text-only start, got %q", status)
	}
}

func TestHandleStartPipeline_WithAnchor(t *testing.T) {
	mr := miniredis.RunT(t)
	database := setupTestDB(t)
	defer database.Close()

	database.Exec(
		`INSERT INTO prompts (seed_text, enriched_text, status, builder_used, metadata)
		 VALUES ('s', 'enriched', 'UNUSED', 'TEST', NULL)`,
	)

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	img := &mockImageProvider{}
	uploader := &mockAssetUploader{uri: "runway://ephemeral/abc"}
	processor := &VideoProcessor{
		DB:                  database,
		Provider:            &mockVideoProvider{taskIDToReturn: "t1"},
		Client:              client,
		ImageProvider:       img,
		Uploader:            uploader,
		DefaultImageAnchors: true,
		VideoOutputDir:      "data/videos",
	}

	if err := processor.HandleStartPipelineTask(context.Background(), asynq.NewTask(TypeStartPipeline, nil)); err != nil {
		t.Fatalf("HandleStartPipelineTask: %v", err)
	}
	if !img.called || !uploader.called {
		t.Fatal("expected image generation and upload")
	}

	var status, meta string
	database.QueryRow("SELECT status, metadata FROM video_jobs LIMIT 1").Scan(&status, &meta)
	if status != "IMAGE_READY" {
		t.Errorf("status = %q, want IMAGE_READY", status)
	}
	if !db.IsProviderImageRef("runway://ephemeral/abc") {
		t.Error("expected provider ref helper to accept runway URI")
	}
}

func TestHandleStartPipeline_UploadFailureMarksFailed(t *testing.T) {
	mr := miniredis.RunT(t)
	database := setupTestDB(t)
	defer database.Close()

	database.Exec(
		`INSERT INTO prompts (seed_text, enriched_text, status, builder_used) VALUES ('s', 'e', 'UNUSED', 'TEST')`,
	)

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer client.Close()

	failUploader := &failUploader{}
	processor := &VideoProcessor{
		DB:                  database,
		Provider:            &mockVideoProvider{},
		Client:              client,
		ImageProvider:       &mockImageProvider{},
		Uploader:            failUploader,
		DefaultImageAnchors: true,
		VideoOutputDir:      "data/videos",
	}

	err := processor.HandleStartPipelineTask(context.Background(), asynq.NewTask(TypeStartPipeline, nil))
	if err == nil {
		t.Fatal("expected upload error")
	}

	var status string
	database.QueryRow("SELECT status FROM video_jobs LIMIT 1").Scan(&status)
	if status != "FAILED" {
		t.Errorf("status = %q, want FAILED", status)
	}
}

type failUploader struct{}

func (f *failUploader) UploadImage(ctx context.Context, localPath string) (string, error) {
	return "", context.DeadlineExceeded
}

var _ video.AssetUploader = (*mockAssetUploader)(nil)
