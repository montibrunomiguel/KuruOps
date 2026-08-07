// Package blobstore abstracts where alert/incident evidence attachments
// (images uploaded via handlers.UploadHandlers) actually live: local disk by
// default, or a tenant-configured S3/GCS bucket once
// service.StorageConfigService.Save* has been called (see Settings ->
// Storage Integration). Every implementation is addressed purely by an
// opaque key string -- callers never see a bucket name, credential, or
// filesystem path directly.
package blobstore

import (
	"context"
	"io"
)

// Store puts and gets objects by key. Put must create any missing
// "directories" implied by the key (local disk) or requires none (S3/GCS,
// which have no real directory concept -- the key is the whole object
// name). Get's returned io.ReadCloser must be closed by the caller; the
// returned content type is whatever the backend recorded at Put time.
type Store interface {
	Put(ctx context.Context, key string, content io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
}

// ErrNotFound is returned by Get when key has no matching object.
var ErrNotFound = notFoundError{}

type notFoundError struct{}

func (notFoundError) Error() string { return "blobstore: object not found" }

// Compile-time checks that every implementation actually satisfies Store --
// S3Store/GCSStore have no unit tests of their own (they'd need live cloud
// credentials or heavy mocking neither the codebase nor the AWS/GCS SDKs
// make easy), so this is the one guarantee that matters without that.
var (
	_ Store = (*LocalStore)(nil)
	_ Store = (*S3Store)(nil)
	_ Store = (*GCSStore)(nil)
)
