package prompt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeminiAdapter_BuildPrompt_Success(t *testing.T) {
	mockResponse := geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{
							Text: `{"prompt": "Enriched cinematic prompt", "youtube_title": "Custom Title", "youtube_description": "SEO Description", "youtube_tags": ["tag1", "tag2"]}`,
						},
					},
				},
			},
		},
		UsageMetadata: geminiUsageMetadata{
			TotalTokenCount: 150,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if r.URL.Query().Get("key") != "mock-api-key" {
			t.Errorf("expected api key query parameter, got %s", r.URL.Query().Get("key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	adapter := NewGeminiAdapter("mock-api-key", "gemini-2.5-flash", 10)
	adapter.baseURL = server.URL // Inject mock server URL

	res, err := adapter.BuildPrompt(context.Background(), PromptBuildRequest{
		SeedPrompt:     "seed",
		TargetProvider: "RUNWAY",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.EnrichedPrompt != "Enriched cinematic prompt" {
		t.Errorf("expected enriched prompt 'Enriched cinematic prompt', got %q", res.EnrichedPrompt)
	}
	if res.TokensUsed != 150 {
		t.Errorf("expected 150 tokens, got %d", res.TokensUsed)
	}
	if res.Metadata["youtube_title"] != "Custom Title" {
		t.Errorf("expected custom title, got %q", res.Metadata["youtube_title"])
	}
	if res.Metadata["youtube_description"] != "SEO Description" {
		t.Errorf("expected SEO description, got %q", res.Metadata["youtube_description"])
	}
	if res.Metadata["youtube_tags"] != `["tag1","tag2"]` {
		t.Errorf("expected tags JSON, got %q", res.Metadata["youtube_tags"])
	}
}

func TestGeminiAdapter_BuildPrompt_Fallback(t *testing.T) {
	mockResponse := geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{
							Text: "Raw cinematic prompt without JSON wrapper.",
						},
					},
				},
			},
		},
		UsageMetadata: geminiUsageMetadata{
			TotalTokenCount: 100,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	adapter := NewGeminiAdapter("mock-api-key", "gemini-2.5-flash", 10)
	adapter.baseURL = server.URL

	res, err := adapter.BuildPrompt(context.Background(), PromptBuildRequest{
		SeedPrompt:     "seed",
		TargetProvider: "RUNWAY",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.EnrichedPrompt != "Raw cinematic prompt without JSON wrapper." {
		t.Errorf("expected raw text, got %q", res.EnrichedPrompt)
	}
	if res.Metadata != nil {
		t.Errorf("expected nil metadata for non-JSON response, got %v", res.Metadata)
	}
}

func TestGeminiAdapter_RateLimit(t *testing.T) {
	adapter := NewGeminiAdapter("mock-api-key", "gemini-2.5-flash", 3)

	// Simulate 3 calls within the last hour
	now := time.Now()
	adapter.callLog = []time.Time{
		now.Add(-10 * time.Minute),
		now.Add(-5 * time.Minute),
		now.Add(-1 * time.Minute),
	}

	// The 4th call should immediately fail with rate limit error
	_, err := adapter.BuildPrompt(context.Background(), PromptBuildRequest{SeedPrompt: "seed"})
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	expectedErr := "rate limit exceeded: 3 calls in the last hour (max 3)"
	if err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}

	// Artificially age one log entry to more than 1 hour ago
	adapter.callLog[0] = now.Add(-65 * time.Minute)

	// Mock server for successful retry after window pruning
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []geminiCandidate{{{Content: geminiContent{Parts: []geminiPart{{Text: "success"}}}}}},
		})
	}))
	defer server.Close()
	adapter.baseURL = server.URL

	res, err := adapter.BuildPrompt(context.Background(), PromptBuildRequest{SeedPrompt: "seed"})
	if err != nil {
		t.Fatalf("unexpected error after rate limit aged: %v", err)
	}
	if res.EnrichedPrompt != "success" {
		t.Errorf("expected 'success', got %q", res.EnrichedPrompt)
	}
}

func TestGeminiAdapter_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": {"message": "Resource has been exhausted", "status": "RESOURCE_EXHAUSTED"}}`))
	}))
	defer server.Close()

	adapter := NewGeminiAdapter("mock-api-key", "gemini-2.5-flash", 10)
	adapter.baseURL = server.URL

	_, err := adapter.BuildPrompt(context.Background(), PromptBuildRequest{SeedPrompt: "seed"})
	if err == nil {
		t.Fatal("expected API error on 429 status code, got nil")
	}
	expectedSub := "gemini API returned 429"
	if !contains(err.Error(), expectedSub) {
		t.Errorf("expected error to contain %q, got %q", expectedSub, err.Error())
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
