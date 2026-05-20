/*
 * internal/adapter/video/upload.go
 *
 * Runway ephemeral image upload (POST /v1/uploads + multipart to uploadUrl).
 *
 * API: https://docs.dev.runwayml.com/assets/uploads
 */
package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

type uploadInitRequest struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
}

type uploadInitResponse struct {
	UploadURL string                 `json:"uploadUrl"`
	Fields    map[string]interface{} `json:"fields"`
	RunwayURI string                 `json:"runwayUri"`
}

/*
 * UploadImage uploads a local image file via Runway ephemeral uploads.
 * Returns a runway:// URI valid for ~24 hours.
 */
func (r *RunwayAdapter) UploadImage(ctx context.Context, localPath string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat image: %w", err)
	}
	if info.Size() < 512 {
		return "", fmt.Errorf("image file too small for Runway upload (min 512 bytes): %s", localPath)
	}

	filename := filepath.Base(localPath)
	initBody, err := json.Marshal(uploadInitRequest{
		Filename: filename,
		Type:     "ephemeral",
	})
	if err != nil {
		return "", fmt.Errorf("marshal upload init: %w", err)
	}

	initURL := fmt.Sprintf("%s/uploads", runwayBaseURL)
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, bytes.NewReader(initBody))
	if err != nil {
		return "", fmt.Errorf("create upload init request: %w", err)
	}
	r.setHeaders(initReq)

	initResp, err := r.client.Do(initReq)
	if err != nil {
		return "", fmt.Errorf("runway upload init HTTP: %w", err)
	}
	defer initResp.Body.Close()

	initRespBody, err := io.ReadAll(initResp.Body)
	if err != nil {
		return "", fmt.Errorf("read upload init response: %w", err)
	}
	if initResp.StatusCode < 200 || initResp.StatusCode >= 300 {
		slog.Error("runway upload init error",
			"status_code", initResp.StatusCode,
			"response_body", string(initRespBody),
		)
		return "", fmt.Errorf("runway upload init returned %d: %s", initResp.StatusCode, string(initRespBody))
	}

	var init uploadInitResponse
	if err := json.Unmarshal(initRespBody, &init); err != nil {
		return "", fmt.Errorf("parse upload init response: %w", err)
	}
	if init.UploadURL == "" || init.RunwayURI == "" {
		return "", fmt.Errorf("runway upload init missing uploadUrl or runwayUri")
	}

	if err := r.postMultipartUpload(ctx, init.UploadURL, init.Fields, localPath, filename); err != nil {
		return "", err
	}

	slog.Info("runway ephemeral image uploaded", "runway_uri", init.RunwayURI, "local_path", localPath)
	return init.RunwayURI, nil
}

/*
 * postMultipartUpload sends the file to Runway's ephemeral storage endpoint.
 */
func (r *RunwayAdapter) postMultipartUpload(ctx context.Context, uploadURL string, fields map[string]interface{}, localPath, filename string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open image for upload: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, val := range fields {
		strVal, ok := val.(string)
		if !ok {
			strVal = fmt.Sprint(val)
		}
		if err := writer.WriteField(key, strVal); err != nil {
			return fmt.Errorf("write upload field %s: %w", key, err)
		}
	}

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy image to form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return fmt.Errorf("create multipart upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("multipart upload HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("runway multipart upload error",
			"status_code", resp.StatusCode,
			"response_body", string(respBody),
		)
		return fmt.Errorf("runway multipart upload returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
