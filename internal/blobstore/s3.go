// Package blobstore provides external (S3/R2/OSS-compatible) storage backends
// for conversation blob bytes, so the bulk of log content lives in cheap object
// storage instead of the Postgres data volume.
package blobstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures an S3-compatible backend (AWS S3, Cloudflare R2, Aliyun/
// Tencent OSS, MinIO, ...).
type S3Config struct {
	Endpoint  string // host only, e.g. "<acct>.r2.cloudflarestorage.com" or "s3.us-east-1.amazonaws.com"
	Region    string // e.g. "auto" for R2, "us-east-1" for S3
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// S3 stores blob bytes in an S3-compatible bucket. It satisfies store.BlobBackend.
type S3 struct {
	cli    *minio.Client
	bucket string
}

// NewS3 builds an S3 backend and verifies the bucket is reachable.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("blobstore: endpoint, bucket, access key and secret key are required")
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	cli, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("blobstore: new client: %w", err)
	}
	// Fail fast if credentials/bucket are wrong.
	if _, err := cli.BucketExists(ctx, cfg.Bucket); err != nil {
		return nil, fmt.Errorf("blobstore: bucket check failed: %w", err)
	}
	return &S3{cli: cli, bucket: cfg.Bucket}, nil
}

// Put uploads compressed bytes under key. It is idempotent (same key overwrites
// identical content-addressed data).
func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.cli.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/zstd"})
	return err
}

// Get downloads the bytes stored under key.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.cli.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// Delete removes the given keys (best-effort per object).
func (s *S3) Delete(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if err := s.cli.RemoveObject(ctx, s.bucket, k, minio.RemoveObjectOptions{}); err != nil {
			return err
		}
	}
	return nil
}
