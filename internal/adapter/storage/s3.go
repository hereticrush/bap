/*
 * internal/adapter/storage/s3.go
 *
 * Implements the StorageProvider interface for S3-compatible endpoints.
 * Supports custom credentials, endpoints, path-style routing, and provides
 * a Stub fallback provider for local-only testing.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

/* S3StorageProvider uploads files to S3-compatible cloud buckets. */
type S3StorageProvider struct {
	client         *s3.Client
	bucket         string
	region         string
	endpoint       string
	forcePathStyle bool
}

/*
 * NewS3StorageProvider configures and initializes an S3 API client using AWS SDK v2.
 */
func NewS3StorageProvider(bucket, region, accessKey, secretKey, endpoint string, forcePathStyle bool) (*S3StorageProvider, error) {
	if bucket == "" {
		return nil, fmt.Errorf("S3 bucket name is required")
	}

	ctx := context.TODO()

	/* Configure SDK credentials and options */
	var optFns []func(*config.LoadOptions) error

	if region != "" {
		optFns = append(optFns, config.WithRegion(region))
	} else {
		optFns = append(optFns, config.WithRegion("us-east-1"))
	}

	if accessKey != "" && secretKey != "" {
		optFns = append(optFns, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("load default config: %w", err)
	}

	/* S3 specific client overrides */
	s3OptFns := []func(*s3.Options){}

	if endpoint != "" {
		s3OptFns = append(s3OptFns, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}

	if forcePathStyle {
		s3OptFns = append(s3OptFns, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(cfg, s3OptFns...)

	return &S3StorageProvider{
		client:         client,
		bucket:         bucket,
		region:         region,
		endpoint:       endpoint,
		forcePathStyle: forcePathStyle,
	}, nil
}

/* Name satisfies StorageProvider. */
func (s *S3StorageProvider) Name() string {
	if s.endpoint != "" {
		return "S3_COMPATIBLE"
	}
	return "S3"
}

/*
 * UploadFile uploads a file to S3 and returns its public URL format.
 */
func (s *S3StorageProvider) UploadFile(ctx context.Context, localPath, contentType string) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open local file: %w", err)
	}
	defer file.Close()

	key := filepath.Base(localPath)

	var contentTypPtr *string
	if contentType != "" {
		contentTypPtr = aws.String(contentType)
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: contentTypPtr,
	})
	if err != nil {
		return "", fmt.Errorf("s3 put object: %w", err)
	}

	/* Construct resource URL based on service type */
	var publicURL string
	if s.endpoint != "" {
		trimmedEP := strings.TrimRight(s.endpoint, "/")
		if s.forcePathStyle {
			publicURL = fmt.Sprintf("%s/%s/%s", trimmedEP, s.bucket, key)
		} else {
			/* Virtual-host style formatting (standard for custom subdomains) */
			if strings.HasPrefix(trimmedEP, "https://") {
				publicURL = fmt.Sprintf("https://%s.%s/%s", s.bucket, strings.TrimPrefix(trimmedEP, "https://"), key)
			} else if strings.HasPrefix(trimmedEP, "http://") {
				publicURL = fmt.Sprintf("http://%s.%s/%s", s.bucket, strings.TrimPrefix(trimmedEP, "http://"), key)
			} else {
				publicURL = fmt.Sprintf("https://%s.%s/%s", s.bucket, trimmedEP, key)
			}
		}
	} else {
		regionStr := s.region
		if regionStr == "" {
			regionStr = "us-east-1"
		}
		publicURL = fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, regionStr, key)
	}

	return publicURL, nil
}

/*
 * DeleteFile removes an asset from S3.
 */
func (s *S3StorageProvider) DeleteFile(ctx context.Context, remoteKey string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(remoteKey),
	})
	if err != nil {
		return fmt.Errorf("s3 delete object: %w", err)
	}
	return nil
}

/* ────────────────────────────────────────────────────────────── */

/* StubStorageProvider is a mock fallback for local tests and local runs. */
type StubStorageProvider struct{}

func NewStubStorageProvider() *StubStorageProvider {
	return &StubStorageProvider{}
}

func (s *StubStorageProvider) Name() string {
	return "STUB"
}

func (s *StubStorageProvider) UploadFile(ctx context.Context, localPath, contentType string) (string, error) {
	return fmt.Sprintf("local://%s", filepath.Base(localPath)), nil
}

func (s *StubStorageProvider) DeleteFile(ctx context.Context, remoteKey string) error {
	return nil
}
