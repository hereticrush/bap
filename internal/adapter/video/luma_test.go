/*
 * internal/adapter/video/luma_test.go
 *
 * Unit tests for the LumaAdapter implementing AIVideoProvider.
 * Uses httptest.Server to mock the Luma REST API to avoid external calls.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hereticrush/bap/internal/adapter/storage"
)

/* MockStorageProvider is a simple mock storage adapter for keyframe uploads. */
type mockStorageProvider struct {
	uploaded bool
}

func (m *mockStorageProvider) Name() string { return "MOCK" }
func (m *mockStorageProvider) UploadFile(ctx context.Context, localPath, contentType string) (string, error) {
	m.uploaded = true
	return "https://cloud-storage.com/anchor.png", nil
}
func (m *mockStorageProvider) DeleteFile(ctx context.Context, remoteKey string) error { return nil }

func TestLumaAdapter_Name(t *testing.T) {
	adapter := NewLumaAdapter("test-key", "ray-2", 10, nil)
	if adapter.Name() != "LUMA" {
		t.Errorf("expected Name 'LUMA', got %q", adapter.Name())
	}
}

func TestLumaAdapter_GenerateVideo_Success(t *testing.T) {
	/* Setup mock HTTP server */
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* Verify request attributes */
		if r.Method != http.MethodPost {
			t.Errorf("expected Method POST, got %q", r.Method)
		}
		if r.URL.Path != "/generations" {
			t.Errorf("expected Path '/generations', got %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer mock-secret-key" {
			t.Errorf("expected Bearer token, got %q", r.Header.Get("Authorization"))
		}

		/* Decode and assert payload */
		var payload lumaCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if payload.Prompt != "Sunset over the lake" {
			t.Errorf("expected prompt 'Sunset over the lake', got %q", payload.Prompt)
		}
		if payload.AspectRatio != "16:9" {
			t.Errorf("expected aspect_ratio '16:9', got %q", payload.AspectRatio)
		}

		/* Write mock response */
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(lumaCreateResponse{ID: "gen_luma_12345"})
	}))
	defer server.Close()

	/* Point base URL to mock server */
	oldURL := lumaBaseURL
	lumaBaseURL = server.URL
	defer func() { lumaBaseURL = oldURL }()

	adapter := NewLumaAdapter("mock-secret-key", "ray-2", 10, nil)
	req := GenerationRequest{
		Prompt:      "Sunset over the lake",
		AspectRatio: "16:9",
	}

	taskID, err := adapter.GenerateVideo(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateVideo returned error: %v", err)
	}
	if taskID != "gen_luma_12345" {
		t.Errorf("expected taskID 'gen_luma_12345', got %q", taskID)
	}
}

func TestLumaAdapter_CheckStatus(t *testing.T) {
	tests := []struct {
		name           string
		mockStatus     string
		mockVideoURL   string
		mockFailure    string
		expectedStatus GenerationStatus
		expectedErr    bool
	}{
		{
			name:           "Queued",
			mockStatus:     "queued",
			expectedStatus: StatusPending,
		},
		{
			name:           "Dreaming",
			mockStatus:     "dreaming",
			expectedStatus: StatusProcessing,
		},
		{
			name:           "Completed",
			mockStatus:     "completed",
			mockVideoURL:   "https://lumacdn.com/out.mp4",
			expectedStatus: StatusCompleted,
		},
		{
			name:           "Failed",
			mockStatus:     "failed",
			mockFailure:    "Prompt violation",
			expectedStatus: StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected Method GET, got %q", r.Method)
				}
				if r.URL.Path != "/generations/task_xyz" {
					t.Errorf("expected Path '/generations/task_xyz', got %q", r.URL.Path)
				}

				var assets *lumaAssets
				if tt.mockVideoURL != "" {
					assets = &lumaAssets{Video: tt.mockVideoURL}
				}

				var failureReason *string
				if tt.mockFailure != "" {
					reason := tt.mockFailure
					failureReason = &reason
				}

				resp := lumaTaskResponse{
					ID:            "task_xyz",
					State:         tt.mockStatus,
					Assets:        assets,
					FailureReason: failureReason,
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			oldURL := lumaBaseURL
			lumaBaseURL = server.URL
			defer func() { lumaBaseURL = oldURL }()

			adapter := NewLumaAdapter("key", "ray-2", 10, nil)
			res, err := adapter.CheckStatus(context.Background(), "task_xyz")
			if err != nil && !tt.expectedErr {
				t.Fatalf("unexpected CheckStatus error: %v", err)
			}

			if res.Status != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, res.Status)
			}
			if tt.mockStatus == "completed" && res.VideoURL != tt.mockVideoURL {
				t.Errorf("expected video URL %q, got %q", tt.mockVideoURL, res.VideoURL)
			}
			if tt.mockStatus == "failed" && res.Error != tt.mockFailure {
				t.Errorf("expected error log %q, got %q", tt.mockFailure, res.Error)
			}
		})
	}
}

func TestLumaAdapter_RateLimiter(t *testing.T) {
	/* Cap at 3 calls per hour */
	adapter := NewLumaAdapter("key", "ray-2", 3, nil)

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

func TestLumaAdapter_UploadImage(t *testing.T) {
	/* 1. Injected StorageProvider mock */
	mockStorage := &mockStorageProvider{}
	adapter := NewLumaAdapter("key", "ray-2", 10, mockStorage)

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
	adapterStub := NewLumaAdapter("key", "ray-2", 10, stubStorage)

	stubUrl, err := adapterStub.UploadImage(context.Background(), "path/to/local_anchor.png")
	if err != nil {
		t.Fatalf("UploadImage with stub storage failed: %v", err)
	}
	if stubUrl != "luma://ephemeral/local_anchor.png" {
		t.Errorf("expected luma:// mock URL prefix under stub mode, got %q", stubUrl)
	}
}
