package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestStorageConfigRepository_S3(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewStorageConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	bucket, region, keyID := "evidence-bucket", "us-east-1", "AKIAEXAMPLE"
	cfg := &domain.StorageConfig{
		TenantID: tenantID, Provider: domain.StorageProviderS3,
		S3Bucket: &bucket, S3Region: &region, S3AccessKeyID: &keyID,
		S3SecretAccessKeySecretRef: "secret://s3-key",
	}
	require.NoError(t, repo.Upsert(t.Context(), tx, cfg))

	t.Run("get after insert", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, domain.StorageProviderS3, got.Provider)
		require.NotNil(t, got.S3Bucket)
		assert.Equal(t, bucket, *got.S3Bucket)
		assert.Equal(t, "secret://s3-key", got.S3SecretAccessKeySecretRef)
	})

	t.Run("upsert switching provider replaces the row, nulling the other provider's fields", func(t *testing.T) {
		gcsBucket, projectID := "gcs-bucket", "my-project"
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderGCS,
			GCSBucket: &gcsBucket, GCSProjectID: &projectID,
			GCSCredentialsJSONSecretRef: "secret://gcs-creds",
		}))

		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Equal(t, domain.StorageProviderGCS, got.Provider)
		assert.Nil(t, got.S3Bucket, "switching to gcs must clear the previous s3 fields")
		require.NotNil(t, got.GCSBucket)
		assert.Equal(t, gcsBucket, *got.GCSBucket)
	})

	t.Run("delete turns the integration off", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx))
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestStorageConfigRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewStorageConfigRepository()

	bucket, region, keyID := "a-bucket", "us-east-1", "AKIAEXAMPLE"
	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.StorageConfig{
		TenantID: tenantA, Provider: domain.StorageProviderS3,
		S3Bucket: &bucket, S3Region: &region, S3AccessKeyID: &keyID,
		S3SecretAccessKeySecretRef: "secret://a",
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.Get(t.Context(), txB)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's storage config")
}
