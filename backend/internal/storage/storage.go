package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store — закрытое S3-совместимое хранилище. Бакет создаётся без публичной политики,
// поэтому объекты доступны только через бэкенд после проверки прав.
type Store struct {
	client *minio.Client
	bucket string
}

func New(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*Store, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &Store{client: c, bucket: bucket}, nil
}

func (s *Store) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if !ok {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}
	}
	// Гарантируем отсутствие публичной политики (бакет закрыт).
	pol, err := s.client.GetBucketPolicy(ctx, s.bucket)
	if err == nil && pol != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(pol), &parsed) == nil {
			_ = s.client.SetBucketPolicy(ctx, s.bucket, "")
		}
	}
	return nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Open возвращает объект, который реализует io.ReadSeeker (нужно для Range-запросов видео).
func (s *Store) Open(ctx context.Context, key string) (*minio.Object, minio.ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, minio.ObjectInfo{}, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, minio.ObjectInfo{}, err
	}
	return obj, info, nil
}

func (s *Store) Stat(ctx context.Context, key string) (minio.ObjectInfo, error) {
	return s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
}

func (s *Store) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *Store) Bucket() string { return s.bucket }
