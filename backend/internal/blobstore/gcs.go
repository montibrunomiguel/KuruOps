package blobstore

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSStore backs Store with a Google Cloud Storage bucket, authenticating
// with an explicit service-account JSON key entered by the admin in
// Settings (see StorageConfigService) rather than Application Default
// Credentials -- ArgusOps is self-hosted and may not be running on GCP at
// all, so there is no ambient credential to fall back on.
type GCSStore struct {
	client *storage.Client
	bucket string
}

func NewGCSStore(ctx context.Context, credentialsJSON, bucket string) (*GCSStore, error) {
	// WithAuthCredentialsJSON(ServiceAccount, ...) rather than the older
	// WithCredentialsJSON: the latter auto-detects the credential *type*
	// from whatever JSON it's handed, including types (e.g.
	// "external_account") that can point at an attacker-controlled token
	// endpoint if the JSON was tampered with -- see the google-api-go-client
	// deprecation notice on WithCredentialsJSON. This admin-supplied blob
	// (Settings -> Storage Integration) is only ever meant to be a plain
	// service-account key, so pinning the accepted type rejects anything
	// else outright instead of trusting whatever type the JSON claims.
	client, err := storage.NewClient(ctx, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(credentialsJSON)))
	if err != nil {
		return nil, err
	}
	return &GCSStore{client: client, bucket: bucket}, nil
}

func (s *GCSStore) Put(ctx context.Context, key string, content io.Reader, _ int64, contentType string) error {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := io.Copy(w, content); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (s *GCSStore) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	r, err := s.client.Bucket(s.bucket).Object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	contentType := r.Attrs.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return r, contentType, nil
}
