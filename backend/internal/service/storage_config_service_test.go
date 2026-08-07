package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/blobstore"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestStorageConfigService_S3(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir)

	t.Run("initial save requires a secret access key", func(t *testing.T) {
		err := svc.SaveS3(t.Context(), tenantID, service.SaveS3Input{Bucket: "b", Region: "us-east-1", AccessKeyID: "AKIA"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveS3(t.Context(), tenantID, service.SaveS3Input{
		Bucket: "evidence", Region: "us-east-1", AccessKeyID: "AKIA", SecretAccessKey: "s3cret",
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotContains(t, cfg.S3SecretAccessKeySecretRef, "s3cret", "the plaintext secret key never lands in the stored ref")

	t.Run("re-saving without a new secret keeps the existing one", func(t *testing.T) {
		require.NoError(t, svc.SaveS3(t.Context(), tenantID, service.SaveS3Input{
			Bucket: "evidence-renamed", Region: "us-east-1", AccessKeyID: "AKIA",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "evidence-renamed", *got.S3Bucket)
		assert.Equal(t, cfg.S3SecretAccessKeySecretRef, got.S3SecretAccessKeySecretRef)
	})
}

func TestStorageConfigService_BuildStore(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir)

	t.Run("no config configured falls back to local disk", func(t *testing.T) {
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.LocalStore)
		assert.True(t, ok, "expected a *blobstore.LocalStore fallback")
	})

	t.Run("s3 configured builds an S3Store", func(t *testing.T) {
		require.NoError(t, svc.SaveS3(t.Context(), tenantID, service.SaveS3Input{
			Bucket: "evidence", Region: "us-east-1", AccessKeyID: "AKIA", SecretAccessKey: "s3cret",
		}))
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.S3Store)
		assert.True(t, ok, "expected a *blobstore.S3Store once S3 is configured")
	})

	t.Run("deleting the config reverts to local disk", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID))
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.LocalStore)
		assert.True(t, ok)
	})
}
