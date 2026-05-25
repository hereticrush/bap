/*
 * internal/adapter/storage/s3_test.go
 *
 * Unit tests for the S3 and Stub storage providers.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package storage

import (
	"context"
	"testing"
)

func TestStubStorageProvider(t *testing.T) {
	ctx := context.Background()
	p := NewStubStorageProvider()

	if p.Name() != "STUB" {
		t.Errorf("expected STUB, got %s", p.Name())
	}

	url, err := p.UploadFile(ctx, "/path/to/test_image.png", "image/png")
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}

	expectedURL := "local://test_image.png"
	if url != expectedURL {
		t.Errorf("expected %s, got %s", expectedURL, url)
	}

	err = p.DeleteFile(ctx, "test_image.png")
	if err != nil {
		t.Errorf("unexpected delete error: %v", err)
	}
}

func TestNewS3StorageProviderValidation(t *testing.T) {
	_, err := NewS3StorageProvider("", "us-east-1", "key", "secret", "https://endpoint.com", true)
	if err == nil {
		t.Error("expected error due to empty bucket name, but got nil")
	}
}
