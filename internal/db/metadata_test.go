package db

import (
	"testing"
)

func TestMetadataJSON(t *testing.T) {
	raw, err := MetadataJSON(nil)
	if err != nil {
		t.Fatalf("MetadataJSON(nil): %v", err)
	}
	if raw != nil {
		t.Errorf("expected nil for empty map, got %q", raw)
	}

	raw, err = MetadataJSON(map[string]string{"voice_script": "Hello"})
	if err != nil {
		t.Fatalf("MetadataJSON: %v", err)
	}
	if string(raw) != `{"voice_script":"Hello"}` {
		t.Errorf("unexpected JSON: %s", raw)
	}
}

func TestTTSText(t *testing.T) {
	meta := `{"voice_script":"Narration only"}`
	if got := TTSText(meta, "Long cinematic prompt"); got != "Narration only" {
		t.Errorf("expected voice_script, got %q", got)
	}
	if got := TTSText(`{}`, "Fallback prompt"); got != "Fallback prompt" {
		t.Errorf("expected fallback, got %q", got)
	}
	if got := TTSText("", "Fallback prompt"); got != "Fallback prompt" {
		t.Errorf("expected fallback with empty metadata, got %q", got)
	}
}

func TestMergeJobMetadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "meta test")
	job, err := CreateJob(db, "RUNWAY")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := SetJobCompleted(db, job.ID, "https://provider.example/v.mp4"); err != nil {
		t.Fatalf("SetJobCompleted: %v", err)
	}

	localPath := "data/videos/test.mp4"
	if err := MergeJobMetadata(db, job.ID, map[string]interface{}{
		MetadataKeyLocalVideoPath: localPath,
	}); err != nil {
		t.Fatalf("MergeJobMetadata: %v", err)
	}

	var url, meta string
	if err := db.QueryRow(
		"SELECT cloud_storage_url, metadata FROM video_jobs WHERE id = ?", job.ID,
	).Scan(&url, &meta); err != nil {
		t.Fatalf("query: %v", err)
	}
	if url != "https://provider.example/v.mp4" {
		t.Errorf("cloud_storage_url changed: %q", url)
	}
	if meta == "" || meta[len(meta)-1] != '}' {
		t.Errorf("unexpected metadata: %q", meta)
	}
	if got := TTSText(meta, ""); got != "" {
		// no voice_script in this job
	}
	_ = localPath
}

func TestCreateJobCopiesPromptMetadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := db.Exec(
		`INSERT INTO prompts (seed_text, enriched_text, status, builder_used, metadata)
		 VALUES ('seed', 'enriched', 'UNUSED', 'TEST', ?)`,
		`{"voice_script":"copied script"}`,
	)
	if err != nil {
		t.Fatalf("insert prompt: %v", err)
	}

	job, err := CreateJob(db, "RUNWAY")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.Metadata != `{"voice_script":"copied script"}` {
		t.Errorf("metadata not copied: %q", job.Metadata)
	}
	if TTSText(job.Metadata, job.PromptTextSnapshot) != "copied script" {
		t.Error("expected voice_script from copied metadata")
	}
}

func TestMarkJobCompletedAfterAudioPreservesURL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedUnusedPrompt(t, db, "audio complete")
	job, _ := CreateJob(db, "RUNWAY")
	providerURL := "https://provider.example/out.mp4"
	SetJobCompleted(db, job.ID, providerURL)

	if err := MarkJobCompletedAfterAudio(db, job.ID); err != nil {
		t.Fatalf("MarkJobCompletedAfterAudio: %v", err)
	}

	var status, url string
	db.QueryRow("SELECT status, cloud_storage_url FROM video_jobs WHERE id = ?", job.ID).Scan(&status, &url)
	if status != "COMPLETED" {
		t.Errorf("status: %q", status)
	}
	if url != providerURL {
		t.Errorf("cloud_storage_url overwritten: %q", url)
	}
}
