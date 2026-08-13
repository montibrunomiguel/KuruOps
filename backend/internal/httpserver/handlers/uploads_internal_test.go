package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/domain"
)

// TestStorageFailureMessage covers uploadFailureMessage's actual decision
// logic in isolation -- no database or tenant needed, since the only real
// causes of a store.Put failure this late (BuildStore already succeeded)
// are either a broken cloud integration or a local-disk problem, and which
// one applies is entirely determined by the tenant's StorageConfig shape.
func TestStorageFailureMessage(t *testing.T) {
	t.Run("no config row -- local disk fallback, not admin-actionable via Settings", func(t *testing.T) {
		assert.Equal(t, "could not store uploaded file -- contact your administrator", storageFailureMessage(nil))
	})

	t.Run("S3 configured -- points at the real cause", func(t *testing.T) {
		msg := storageFailureMessage(&domain.StorageConfig{Provider: domain.StorageProviderS3})
		assert.Contains(t, msg, "Settings -> Storage Integration")
	})

	t.Run("GCS configured -- points at the real cause", func(t *testing.T) {
		msg := storageFailureMessage(&domain.StorageConfig{Provider: domain.StorageProviderGCS})
		assert.Contains(t, msg, "Settings -> Storage Integration")
	})

	t.Run("unrecognized provider -- same fallback as no config, matching BuildStore's own default case", func(t *testing.T) {
		assert.Equal(t, "could not store uploaded file -- contact your administrator", storageFailureMessage(&domain.StorageConfig{Provider: "unknown"}))
	})
}
