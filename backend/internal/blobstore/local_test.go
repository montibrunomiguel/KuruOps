package blobstore_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/blobstore"
)

func TestLocalStore_PutAndGet(t *testing.T) {
	store := blobstore.NewLocalStore(t.TempDir())

	err := store.Put(context.Background(), "Alert/2026/08/04/id_Title/file.png", strings.NewReader("hello"), 5, "image/png")
	require.NoError(t, err)

	rc, contentType, err := store.Get(context.Background(), "Alert/2026/08/04/id_Title/file.png")
	require.NoError(t, err)
	defer rc.Close()

	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	assert.Equal(t, "image/png", contentType)
}

func TestLocalStore_PutCreatesMissingDirectories(t *testing.T) {
	store := blobstore.NewLocalStore(t.TempDir())

	err := store.Put(context.Background(), "Incident/2026/08/04/id_Name/deep/nested/file.jpg", strings.NewReader("x"), 1, "image/jpeg")
	require.NoError(t, err)

	rc, _, err := store.Get(context.Background(), "Incident/2026/08/04/id_Name/deep/nested/file.jpg")
	require.NoError(t, err)
	rc.Close()
}

func TestLocalStore_GetMissingKeyReturnsErrNotFound(t *testing.T) {
	store := blobstore.NewLocalStore(t.TempDir())

	_, _, err := store.Get(context.Background(), "Alert/2026/08/04/id_Title/missing.png")
	assert.ErrorIs(t, err, blobstore.ErrNotFound)
}
