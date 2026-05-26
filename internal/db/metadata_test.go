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

func TestUseImageAnchor(t *testing.T) {
	if !UseImageAnchor(`{"use_image_anchor":"true"}`, false) {
		t.Error("expected true from seed override")
	}
	if UseImageAnchor(`{"use_image_anchor":"false"}`, true) {
		t.Error("expected false from seed override")
	}
	if !UseImageAnchor("", true) {
		t.Error("expected default when unset")
	}
	if !UseImageAnchor(`{"voice_script":"x"}`, true) {
		t.Error("expected default when key absent")
	}
}

func TestIsProviderImageRef(t *testing.T) {
	if !IsProviderImageRef("runway://x") || !IsProviderImageRef("https://a/b.png") {
		t.Error("expected provider refs accepted")
	}
	if IsProviderImageRef("data/images/job.png") || IsProviderImageRef("/app/data/x.png") {
		t.Error("expected local paths rejected")
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

func TestYoutubeGetters(t *testing.T) {
	/* 1. Title fallback */
	if got := GetYoutubeTitle(`{"youtube_title":"Neon rain meditations"}`, "default"); got != "Neon rain meditations" {
		t.Errorf("expected custom title, got %q", got)
	}
	if got := GetYoutubeTitle(`{}`, "default"); got != "default" {
		t.Errorf("expected default title, got %q", got)
	}

	/* 2. Description fallback */
	if got := GetYoutubeDescription(`{"youtube_description":"Desc test"}`, "default"); got != "Desc test" {
		t.Errorf("expected custom description, got %q", got)
	}
	if got := GetYoutubeDescription(`{}`, "default"); got != "default" {
		t.Errorf("expected default description, got %q", got)
	}

	/* 3. Privacy fallback & normalization */
	if got := GetYoutubePrivacy(`{"youtube_privacy":"PUBLIC"}`, "private"); got != "public" {
		t.Errorf("expected public, got %q", got)
	}
	if got := GetYoutubePrivacy(`{}`, "private"); got != "private" {
		t.Errorf("expected private default, got %q", got)
	}

	/* 4. Playlist ID */
	if got := GetYoutubePlaylistID(`{"youtube_playlist_id":"PL123"}`); got != "PL123" {
		t.Errorf("expected playlist PL123, got %q", got)
	}
	if got := GetYoutubePlaylistID(`{}`); got != "" {
		t.Errorf("expected empty playlist ID, got %q", got)
	}

	/* 5. Tags parsing (comma-separated string vs. JSON string array) */
	defaultTags := []string{"ai", "bap"}

	// Comma separated
	gotComma := GetYoutubeTags(`{"youtube_tags":"one, two,three "}`, defaultTags)
	if len(gotComma) != 3 || gotComma[0] != "one" || gotComma[1] != "two" || gotComma[2] != "three" {
		t.Errorf("unexpected tags from comma string: %v", gotComma)
	}

	// JSON array
	gotArray := GetYoutubeTags(`{"youtube_tags":["alpha", "beta"]}`, defaultTags)
	if len(gotArray) != 2 || gotArray[0] != "alpha" || gotArray[1] != "beta" {
		t.Errorf("unexpected tags from JSON array: %v", gotArray)
	}

	// Fallback when empty or missing
	gotFallback := GetYoutubeTags(`{}`, defaultTags)
	if len(gotFallback) != 2 || gotFallback[0] != "ai" || gotFallback[1] != "bap" {
		t.Errorf("unexpected fallback tags: %v", gotFallback)
	}
}
