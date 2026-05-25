/*
 * internal/adapter/storage/provider.go
 *
 * Defines the generic interface for storage providers used by BAP.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package storage

import "context"

/*
 * StorageProvider abstracts the cloud storage system.
 * Any cloud service (S3, Cloudflare R2, Google Cloud Storage) can be
 * integrated by implementing this contract.
 */
type StorageProvider interface {
	/* Name returns the provider identifier (e.g., "S3", "R2"). */
	Name() string

	/*
	 * UploadFile uploads a local file to the cloud bucket.
	 * Returns the resulting public/private download URL.
	 */
	UploadFile(ctx context.Context, localPath, contentType string) (string, error)

	/*
	 * DeleteFile deletes a file from the cloud bucket by its remote key/name.
	 */
	DeleteFile(ctx context.Context, remoteKey string) error
}
