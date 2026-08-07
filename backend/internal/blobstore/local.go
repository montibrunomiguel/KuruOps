package blobstore

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// LocalStore writes objects under a root directory on local disk -- the
// fallback used whenever a tenant has no S3/GCS integration configured
// (UPLOAD_DIR, see internal/config). Unlike S3/GCS, plain files carry no
// built-in content-type metadata, so Get infers it from the key's
// extension instead of storing it separately.
type LocalStore struct {
	Dir string
}

func NewLocalStore(dir string) *LocalStore {
	return &LocalStore{Dir: dir}
}

func (s *LocalStore) Put(_ context.Context, key string, content io.Reader, _ int64, _ string) error {
	path := filepath.Join(s.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, content)
	return err
}

func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	path := filepath.Join(s.Dir, filepath.FromSlash(key))
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	contentType := mime.TypeByExtension(filepath.Ext(key))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return f, contentType, nil
}
