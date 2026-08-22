package blobstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// newTestGDriveStore builds a GDriveStore pointed at a local test server
// standing in for the Drive v3 REST API -- lives in-package (not
// blobstore_test) so it can construct GDriveStore directly via its
// unexported fields, bypassing NewGDriveStoreFromServiceAccount/OAuth's
// real credential handshakes (neither is reachable without live Google
// credentials). A trailing slash on the endpoint matters: Drive's generated
// client resolves relative paths ("files", "files/{fileId}") against
// BasePath using RFC 3986 reference resolution, which replaces the last
// path segment of a base with no trailing slash instead of appending to it.
func newTestGDriveStore(t *testing.T, handler http.Handler) *GDriveStore {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	svc, err := drive.NewService(context.Background(),
		option.WithEndpoint(srv.URL+"/"),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)
	return &GDriveStore{svc: svc, folderID: "folder-1"}
}

func TestGDriveStore_Put_CreatesNewFile(t *testing.T) {
	var gotCreate bool
	store := newTestGDriveStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/files":
			// findFileID's search -- nothing exists yet, so Put must create.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == "POST" && r.URL.Path == "/upload/drive/v3/files":
			gotCreate = true
			assert.Equal(t, "multipart", r.URL.Query().Get("uploadType"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"new-file-id"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	err := store.Put(t.Context(), "evidence.png", strings.NewReader("fake image bytes"), 17, "image/png")
	require.NoError(t, err)
	assert.True(t, gotCreate, "expected a create (POST) request since no existing file was found")
}

func TestGDriveStore_Put_UpdatesExistingFile(t *testing.T) {
	var gotUpdate bool
	store := newTestGDriveStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/files":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[{"id":"existing-file-id"}]}`))
		case r.Method == "PATCH" && r.URL.Path == "/upload/drive/v3/files/existing-file-id":
			gotUpdate = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"existing-file-id"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	err := store.Put(t.Context(), "evidence.png", strings.NewReader("updated bytes"), 13, "image/png")
	require.NoError(t, err)
	assert.True(t, gotUpdate, "expected an update (PATCH) request since a matching file already existed")
}

func TestGDriveStore_Get_Found(t *testing.T) {
	store := newTestGDriveStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/files":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[{"id":"file-1"}]}`))
		case r.Method == "GET" && r.URL.Path == "/files/file-1" && r.URL.Query().Get("alt") == "media":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("the file contents"))
		case r.Method == "GET" && r.URL.Path == "/files/file-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file-1","mimeType":"text/plain"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	rc, contentType, err := store.Get(t.Context(), "evidence.png")
	require.NoError(t, err)
	defer rc.Close()
	assert.Equal(t, "text/plain", contentType)
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "the file contents", string(body))
}

func TestGDriveStore_Get_NotFound(t *testing.T) {
	store := newTestGDriveStore(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[]}`))
	}))

	_, _, err := store.Get(t.Context(), "missing.png")
	assert.ErrorIs(t, err, ErrNotFound)
}

// driveTestServiceAccountJSON is a syntactically-valid but entirely fake
// service-account key -- google.golang.org/api's client construction
// parses the key's shape but doesn't sign/verify anything until an actual
// token request is made, so this is enough to exercise
// NewGDriveStoreFromServiceAccount without live credentials (confirmed
// empirically; see storage_config_service_test.go's identical fixture for
// the same reasoning).
const driveTestServiceAccountJSON = `{
	"type": "service_account",
	"project_id": "test-project",
	"private_key_id": "abc123",
	"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC1\n-----END PRIVATE KEY-----\n",
	"client_email": "test@test-project.iam.gserviceaccount.com",
	"client_id": "123456789",
	"token_uri": "https://oauth2.googleapis.com/token"
}`

func TestNewGDriveStoreFromServiceAccount(t *testing.T) {
	store, err := NewGDriveStoreFromServiceAccount(t.Context(), driveTestServiceAccountJSON, "folder-1")
	require.NoError(t, err)
	assert.Equal(t, "folder-1", store.folderID)
	assert.NotNil(t, store.svc)
}

func TestNewGDriveStoreFromServiceAccount_InvalidJSON(t *testing.T) {
	_, err := NewGDriveStoreFromServiceAccount(t.Context(), "not valid json", "folder-1")
	assert.Error(t, err)
}

func TestNewGDriveStoreFromOAuth(t *testing.T) {
	// Building the client from a refresh token doesn't itself contact
	// Google -- oauth2.Config.TokenSource lazily refreshes on first actual
	// use, so a fake refresh token is enough to exercise construction.
	store, err := NewGDriveStoreFromOAuth(t.Context(), "client-id", "client-secret", "fake-refresh-token", "folder-2")
	require.NoError(t, err)
	assert.Equal(t, "folder-2", store.folderID)
	assert.NotNil(t, store.svc)
}

func TestEscapeDriveQueryValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain-name.png", "plain-name.png"},
		{"a'quote", `a\'quote`},
		{`back\slash`, `back\\slash`},
		{`both\'chars`, `both\\\'chars`},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, escapeDriveQueryValue(tc.in))
	}
}
