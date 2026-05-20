package video

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunwayAdapter_UploadImage(t *testing.T) {
	tmp := t.TempDir()
	imagePath := filepath.Join(tmp, "anchor.png")
	if err := os.WriteFile(imagePath, []byte(strings.Repeat("x", 600)), 0644); err != nil {
		t.Fatal(err)
	}

	const wantURI = "runway://ephemeral/test-uri"

	var gotMultipart bool
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		gotMultipart = true
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/uploads" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req uploadInitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("init body: %v", err)
		}
		if req.Filename != "anchor.png" || req.Type != "ephemeral" {
			t.Errorf("unexpected init request: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(uploadInitResponse{
			UploadURL: uploadSrv.URL,
			Fields:    map[string]interface{}{"key": "value"},
			RunwayURI: wantURI,
		})
	}))
	defer apiSrv.Close()

	adapter := NewRunwayAdapter("test-key", "gen3a_turbo", 100)
	adapter.client = apiSrv.Client()

	origBase := runwayBaseURL
	runwayBaseURL = apiSrv.URL + "/v1"
	t.Cleanup(func() { runwayBaseURL = origBase })

	uri, err := adapter.UploadImage(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("UploadImage: %v", err)
	}
	if uri != wantURI {
		t.Errorf("uri = %q, want %q", uri, wantURI)
	}
	if !gotMultipart {
		t.Error("expected multipart upload to storage endpoint")
	}
}
