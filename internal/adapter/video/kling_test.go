/*
 * internal/adapter/video/kling_test.go
 *
 * Unit tests for the KlingAdapter implementing AIVideoProvider.
 * Uses httptest.Server to mock the Kling REST API to avoid external calls.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hereticrush/bap/internal/adapter/storage"
)

func TestKlingAdapter_Name(t *testing.T) {
	adapter := NewKlingAdapter("test-key", "kling-v3", 10, nil)
	if adapter.Name() != "KLING" {
		t.Errorf("expected Name 'KLING', got %q", adapter.Name())
	}
}

func TestKlingAdapter_GenerateVideo_Text2Video_Success(t *testing.T) {
	/* Setup mock HTTP server */
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* Verify request attributes */
		if r.Method != http.MethodPost {
			t.Errorf("expected Method POST, got %q", r.Method)
		}
		if r.URL.Path != "/v1/videos/text2video" {
			t.Errorf("expected Path '/v1/videos/text2video', got %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer mock-secret-key" {
			t.Errorf("expected Bearer token, got %q", r.Header.Get("Authorization"))
		}

		/* Decode and assert payload */
		var payload klingText2VideoRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if payload.Prompt != "Cinematic sunset" {
			t.Errorf("expected prompt 'Cinematic sunset', got %q", payload.Prompt)
		}
		if payload.AspectRatio != "16:9" {
			t.Errorf("expected aspect_ratio '16:9', got %q", payload.AspectRatio)
		}

		/* Write mock response */
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := klingCreateResponse{
			Code:    0,
			Message: "success",
		}
		resp.Data.TaskID = "task_text_12345"
		resp.Data.TaskStatus = "submitted"
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	/* Point base URL to mock server */
	oldURL := klingBaseURL
	klingBaseURL = server.URL
	defer func() { klingBaseURL = oldURL }()

	adapter := NewKlingAdapter("mock-secret-key", "kling-v3", 10, nil)
	req := GenerationRequest{
		Prompt:      "Cinematic sunset",
		AspectRatio: "16:9",
	}

	taskID, err := adapter.GenerateVideo(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateVideo returned error: %v", err)
	}
	if taskID != "task_text_12345" {
		t.Errorf("expected taskID 'task_text_12345', got %q", taskID)
	}
}

func TestKlingAdapter_GenerateVideo_Image2Video_Success(t *testing.T) {
	/* Setup mock HTTP server */
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* Verify request attributes */
		if r.Method != http.MethodPost {
			t.Errorf("expected Method POST, got %q", r.Method)
		}
		if r.URL.Path != "/v1/videos/image2video" {
			t.Errorf("expected Path '/v1/videos/image2video', got %q", r.URL.Path)
		}

		/* Decode and assert payload */
		var payload klingImage2VideoRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if payload.Image != "https://cloud-storage.com/anchor.png" {
			t.Errorf("expected image URL, got %q", payload.Image)
		}

		/* Write mock response */
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := klingCreateResponse{
			Code:    0,
			Message: "success",
		}
		resp.Data.TaskID = "task_img_12345"
		resp.Data.TaskStatus = "submitted"
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	/* Point base URL to mock server */
	oldURL := klingBaseURL
	klingBaseURL = server.URL
	defer func() { klingBaseURL = oldURL }()

	adapter := NewKlingAdapter("mock-secret-key", "kling-v3", 10, nil)
	req := GenerationRequest{
		Prompt:      "Cinematic sunset",
		AspectRatio: "16:9",
		ImageURLs:   []string{"https://cloud-storage.com/anchor.png"},
	}

	taskID, err := adapter.GenerateVideo(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateVideo returned error: %v", err)
	}
	if taskID != "task_img_12345" {
		t.Errorf("expected taskID 'task_img_12345', got %q", taskID)
	}
}

func TestKlingAdapter_CheckStatus(t *testing.T) {
	tests := []struct {
		name           string
		mockStatus     string
		mockVideoURL   string
		mockErrorMsg   string
		expectedStatus GenerationStatus
		expectedErr    bool
	}{
		{
			name:           "Submitted",
			mockStatus:     "submitted",
			expectedStatus: StatusPending,
		},
		{
			name:           "Processing",
			mockStatus:     "processing",
			expectedStatus: StatusProcessing,
		},
		{
			name:           "Succeed",
			mockStatus:     "succeed",
			mockVideoURL:   "https://klingcdn.com/out.mp4",
			expectedStatus: StatusCompleted,
		},
		{
			name:           "Failed",
			mockStatus:     "failed",
			mockErrorMsg:   "Content violation",
			expectedStatus: StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected Method GET, got %q", r.Method)
				}
				if r.URL.Path != "/v1/videos/status/task_xyz" {
					t.Errorf("expected Path '/v1/videos/status/task_xyz', got %q", r.URL.Path)
				}

				resp := klingTaskResponse{
					Code:    0,
					Message: "success",
				}
				resp.Data.TaskID = "task_xyz"
				resp.Data.TaskStatus = tt.mockStatus
				resp.Data.ErrorMessage = tt.mockErrorMsg

				if tt.mockVideoURL != "" {
					resp.Data.TaskResult = &struct {
						Videos []struct {
							ID       string `json:"id"`
							URL      string `json:"url"`
							Duration string `json:"duration"`
						} `json:"videos"`
					}{
						Videos: []struct {
							ID       string `json:"id"`
							URL      string `json:"url"`
							Duration string `json:"duration"`
						}{
							{
								ID:       "vid_123",
								URL:      tt.mockVideoURL,
								Duration: "5",
							},
						},
					}
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			oldURL := klingBaseURL
			klingBaseURL = server.URL
			defer func() { klingBaseURL = oldURL }()

			adapter := NewKlingAdapter("key", "kling-v3", 10, nil)
			res, err := adapter.CheckStatus(context.Background(), "task_xyz")
			if err != nil && !tt.expectedErr {
				t.Fatalf("unexpected CheckStatus error: %v", err)
			}

			if res.Status != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, res.Status)
			}
			if tt.mockStatus == "succeed" && res.VideoURL != tt.mockVideoURL {
				t.Errorf("expected video URL %q, got %q", tt.mockVideoURL, res.VideoURL)
			}
			if tt.mockStatus == "failed" && res.Error != tt.mockErrorMsg {
				t.Errorf("expected error log %q, got %q", tt.mockErrorMsg, res.Error)
			}
		})
	}
}

func TestKlingAdapter_RateLimiter(t *testing.T) {
	/* Cap at 3 calls per hour */
	adapter := NewKlingAdapter("key", "kling-v3", 3, nil)

	/* Populate rate limiter */
	adapter.callLog = []time.Time{
		time.Now().Add(-10 * time.Minute),
		time.Now().Add(-5 * time.Minute),
		time.Now().Add(-2 * time.Minute),
	}

	err := adapter.checkRateLimit()
	if err == nil {
		t.Error("expected rate limit exceeded error, got nil")
	}

	/* Prune check */
	adapter.callLog = []time.Time{
		time.Now().Add(-70 * time.Minute), // Expired
		time.Now().Add(-5 * time.Minute),
		time.Now().Add(-2 * time.Minute),
	}

	err = adapter.checkRateLimit()
	if err != nil {
		t.Errorf("unexpected rate limit error after prune: %v", err)
	}
}

func TestKlingAdapter_UploadImage(t *testing.T) {
	/* 1. Injected StorageProvider mock */
	mockStorage := &mockStorageProvider{}
	adapter := NewKlingAdapter("key", "kling-v3", 10, mockStorage)

	url, err := adapter.UploadImage(context.Background(), "path/to/anchor.png")
	if err != nil {
		t.Fatalf("UploadImage failed: %v", err)
	}
	if !mockStorage.uploaded {
		t.Error("expected UploadFile on mock storage provider to be called")
	}
	if url != "https://cloud-storage.com/anchor.png" {
		t.Errorf("expected public URL, got %q", url)
	}

	/* 2. Injected StubStorageProvider */
	stubStorage := storage.NewStubStorageProvider()
	adapterStub := NewKlingAdapter("key", "kling-v3", 10, stubStorage)

	stubUrl, err := adapterStub.UploadImage(context.Background(), "path/to/local_anchor.png")
	if err != nil {
		t.Fatalf("UploadImage with stub storage failed: %v", err)
	}
	if stubUrl != "kling://ephemeral/local_anchor.png" {
		t.Errorf("expected kling:// mock URL prefix under stub mode, got %q", stubUrl)
	}
}
