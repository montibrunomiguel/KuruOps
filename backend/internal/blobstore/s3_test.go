package blobstore

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestS3Store builds an S3Store pointed at a local test server standing
// in for the S3 REST API -- lives in-package (not blobstore_test) so it can
// construct S3Store directly via its unexported fields, matching gdrive_test.go/
// gcs_test.go's approach for their own SDK clients. UsePathStyle is required
// here specifically: the SDK's default virtual-hosted-style addressing puts
// the bucket in the Host header (bucket.<endpoint>), which would need real
// DNS resolution for a plain httptest server's host:port -- path-style keeps
// the bucket in the URL path instead (GET/PUT /{bucket}/{key}), which works
// against any endpoint.
func newTestS3Store(t *testing.T, handler http.Handler) *S3Store {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test-key", "test-secret", ""),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	return &S3Store{client: client, bucket: "test-bucket"}
}

func TestS3Store_Put(t *testing.T) {
	var gotPut bool
	store := newTestS3Store(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/test-bucket/evidence.png" {
			gotPut = true
			body, _ := io.ReadAll(r.Body)
			assert.Equal(t, "fake image bytes", string(body))
			assert.Equal(t, "image/png", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))

	const body = "fake image bytes"
	err := store.Put(t.Context(), "evidence.png", strings.NewReader(body), int64(len(body)), "image/png")
	require.NoError(t, err)
	assert.True(t, gotPut, "expected a PUT request to the object's path-style URL")
}

func TestS3Store_Get_Found(t *testing.T) {
	store := newTestS3Store(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// A "defaults Content-Type when the response header is absent" test isn't
// reproducible against net/http's ResponseWriter: it auto-sniffs a
// Content-Type via http.DetectContentType whenever the header was never
// set before the first Write, so a genuinely nil out.ContentType (the
// only case s3.go's fallback branch actually covers) can't be triggered
// through httptest.Server -- only through a raw, hand-rolled response
// writer, which isn't worth the added fragility for this one branch.

// TestS3Store_Get_NoSuchKey exercises the ErrNotFound mapping -- the S3
// REST API reports a missing key as a 404 with an XML error body whose
// <Code> the SDK maps to the modeled *types.NoSuchKey exception (only
// GetObject declares this error in its model; other missing-resource cases
// come back as a generic error instead, see the next test).
func TestS3Store_Get_NoSuchKey(t *testing.T) {
	store := newTestS3Store(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <Key>evidence.png</Key>
  <RequestId>test-request-id</RequestId>
</Error>`))
	}))

	_, _, err := store.Get(t.Context(), "missing.png")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestS3Store_Get_OtherErrorPassesThrough(t *testing.T) {
	store := newTestS3Store(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>AccessDenied</Code>
  <Message>Access Denied</Message>
  <RequestId>test-request-id</RequestId>
</Error>`))
	}))

	_, _, err := store.Get(t.Context(), "forbidden.png")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "a non-NoSuchKey failure must not be misreported as ErrNotFound")
}
