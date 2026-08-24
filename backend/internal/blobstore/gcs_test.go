package blobstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// newTestGCSStore builds a GCSStore pointed at a local test server standing
// in for the GCS JSON/media APIs -- lives in-package (not blobstore_test)
// so it can construct GCSStore directly via its unexported fields,
// bypassing NewGCSStore's real service-account credential handshake
// (unreachable without live Google credentials). Same reasoning and
// approach as gdrive_test.go's newTestGDriveStore.
//
// Which endpoint a given request hits was found empirically (a throwaway
// capture handler logging method/path/body), not by reading the client
// source: a small Put (well under the resumable-upload size threshold)
// sends one multipart POST to /upload/storage/v1/b/{bucket}/o, and Get
// hits the plain media-download path GET /{bucket}/{key} -- neither
// matches the JSON API's documented resource paths exactly, since the Go
// client picks whichever transport is cheapest for the operation.
func newTestGCSStore(t *testing.T, handler http.Handler) *GCSStore {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := storage.NewClient(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)
	return &GCSStore{client: client, bucket: "test-bucket"}
}

func TestGCSStore_Put(t *testing.T) {
	var gotUpload bool
	store := newTestGCSStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload/storage/v1/b/test-bucket/o") {
			gotUpload = true
			assert.Equal(t, "evidence.png", r.URL.Query().Get("name"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"evidence.png","bucket":"test-bucket","contentType":"image/png"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))

	err := store.Put(t.Context(), "evidence.png", strings.NewReader("fake image bytes"), 17, "image/png")
	require.NoError(t, err)
	assert.True(t, gotUpload, "expected an upload POST request")
}

func TestGCSStore_Get_Found(t *testing.T) {
	store := newTestGCSStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/test-bucket/evidence.png", r.URL.Path)
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake image bytes"))
	}))

	rc, contentType, err := store.Get(t.Context(), "evidence.png")
	require.NoError(t, err)
	defer rc.Close()

	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "fake image bytes", string(body))
	assert.Equal(t, "image/png", contentType)
}

func TestGCSStore_Get_DefaultsContentTypeWhenMissing(t *testing.T) {
	store := newTestGCSStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Explicitly empty (not just omitted) -- net/http's ResponseWriter
		// auto-sniffs a Content-Type via http.DetectContentType whenever the
		// header was never touched at all before the first Write, which
		// would silently defeat this test's whole point.
		w.Header().Set("Content-Type", "")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bytes"))
	}))

	_, contentType, err := store.Get(t.Context(), "evidence.png")
	require.NoError(t, err)
	assert.Equal(t, "application/octet-stream", contentType)
}

func TestGCSStore_Get_NotFound(t *testing.T) {
	store := newTestGCSStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"not found"}}`))
	}))

	_, _, err := store.Get(t.Context(), "missing.png")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGCSStore_Get_OtherErrorPassesThrough(t *testing.T) {
	store := newTestGCSStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"forbidden"}}`))
	}))

	_, _, err := store.Get(t.Context(), "forbidden.png")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "a non-404 failure must not be misreported as ErrNotFound")
}
