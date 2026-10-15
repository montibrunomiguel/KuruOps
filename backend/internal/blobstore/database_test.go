package blobstore_test

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"strings"

	"github.com/kuruops/kuruops/internal/blobstore"
	"github.com/kuruops/kuruops/internal/testutil"
)

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

func TestDatabaseStore_RoundTrip(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenant := testutil.NewTenant(t)
	s := blobstore.NewDatabaseStore(pool, tenant)

	require.NoError(t, s.Put(t.Context(), "Alert/2026/10/15/x/abc_f.png", strings.NewReader("PNGDATA"), 7, "image/png"))

	rc, ct, err := s.Get(t.Context(), "Alert/2026/10/15/x/abc_f.png")
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", readAll(t, rc))
	assert.Equal(t, "image/png", ct, "the content type recorded at Put time comes back, not one guessed from the extension")
}

func TestDatabaseStore_MissingKeyIsNotFound(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	s := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))

	_, _, err := s.Get(t.Context(), "Alert/2026/10/15/x/none.png")
	assert.ErrorIs(t, err, blobstore.ErrNotFound)
}

func TestDatabaseStore_PutOverwrites(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	s := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))

	require.NoError(t, s.Put(t.Context(), "k.png", strings.NewReader("one"), 3, "image/png"))
	require.NoError(t, s.Put(t.Context(), "k.png", strings.NewReader("second"), 6, "image/png"))

	rc, _, err := s.Get(t.Context(), "k.png")
	require.NoError(t, err)
	assert.Equal(t, "second", readAll(t, rc))
}

// TestDatabaseStore_TenantsCannotReadEachOthersAttachments pins row-level security:
// the same key under two tenants is two objects, and one tenant's store never sees the
// other's.
func TestDatabaseStore_TenantsCannotReadEachOthersAttachments(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	a := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))
	b := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))

	require.NoError(t, a.Put(t.Context(), "shared-key.png", strings.NewReader("A-secret"), 8, "image/png"))

	_, _, err := b.Get(t.Context(), "shared-key.png")
	assert.ErrorIs(t, err, blobstore.ErrNotFound, "tenant B must not read tenant A's object")

	require.NoError(t, b.Put(t.Context(), "shared-key.png", strings.NewReader("B-data"), 6, "image/png"))
	rc, _, err := a.Get(t.Context(), "shared-key.png")
	require.NoError(t, err)
	assert.Equal(t, "A-secret", readAll(t, rc), "B writing the same key must not overwrite A's")
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDatabaseStore_APartialReadStoresNothing(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	s := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))

	require.Error(t, s.Put(t.Context(), "broken.png", failingReader{}, 10, "image/png"))
	_, _, err := s.Get(t.Context(), "broken.png")
	assert.ErrorIs(t, err, blobstore.ErrNotFound)
}

// TestFallbackStore covers the move from local disk to the database: old objects must
// stay readable, new ones go to the primary.
func TestFallbackStore(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	dir := t.TempDir()
	local := blobstore.NewLocalStore(dir)
	db := blobstore.NewDatabaseStore(pool, testutil.NewTenant(t))
	s := blobstore.NewFallbackStore(db, local)

	require.NoError(t, local.Put(t.Context(), "old/legacy.png", strings.NewReader("LEGACY"), 6, ""))
	require.NoError(t, s.Put(t.Context(), "new/fresh.png", strings.NewReader("FRESH"), 5, "image/png"))

	rc, _, err := s.Get(t.Context(), "old/legacy.png")
	require.NoError(t, err, "an object uploaded before the switch is still served")
	assert.Equal(t, "LEGACY", readAll(t, rc))

	rc, ct, err := s.Get(t.Context(), "new/fresh.png")
	require.NoError(t, err)
	assert.Equal(t, "FRESH", readAll(t, rc))
	assert.Equal(t, "image/png", ct)

	// New writes go to the primary, never to the old volume.
	_, err = os.Stat(dir + "/new/fresh.png")
	assert.True(t, os.IsNotExist(err), "writes must not touch the fallback")

	_, _, err = s.Get(t.Context(), "nowhere.png")
	assert.ErrorIs(t, err, blobstore.ErrNotFound)
}
