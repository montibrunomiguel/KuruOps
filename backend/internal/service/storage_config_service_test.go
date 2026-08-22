package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/blobstore"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// newStorageConfigServiceWithOAuth wires a real OAuthStateService plus a
// (fake, unreachable-endpoint) Google OAuth client -- everything short of
// the actual network exchange with Google, which the OAuth happy path
// itself has no unit-test coverage of here for the same reason
// TestStorageConfigService_BuildStore's GCS case doesn't build a real
// cloud client: no live credentials/network in this test run. What's
// covered is everything this service controls -- state generation/
// validation, and every error path before the exchange.
func newStorageConfigServiceWithOAuth(t *testing.T, dir string) *service.StorageConfigService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	oauthStates := service.NewOAuthStateService(pool, repository.NewOAuthStateRepository())
	return service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir,
		oauthStates, "test-client-id", "test-client-secret", "https://argusops.example/auth/oauth/gdrive/callback")
}

func TestStorageConfigService_S3(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "")

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

func TestStorageConfigService_GCS(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "")

	t.Run("initial save requires credentials JSON", func(t *testing.T) {
		err := svc.SaveGCS(t.Context(), tenantID, service.SaveGCSInput{Bucket: "b", ProjectID: "p"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveGCS(t.Context(), tenantID, service.SaveGCSInput{
		Bucket: "evidence", ProjectID: "argusops-prod", CredentialsJSON: `{"type":"service_account"}`,
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "argusops-prod", *cfg.GCSProjectID)
	assert.NotContains(t, cfg.GCSCredentialsJSONSecretRef, "service_account", "the plaintext credentials JSON never lands in the stored ref")

	t.Run("re-saving without new credentials keeps the existing ones", func(t *testing.T) {
		require.NoError(t, svc.SaveGCS(t.Context(), tenantID, service.SaveGCSInput{
			Bucket: "evidence-renamed", ProjectID: "argusops-prod",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "evidence-renamed", *got.GCSBucket)
		assert.Equal(t, cfg.GCSCredentialsJSONSecretRef, got.GCSCredentialsJSONSecretRef)
	})
}

func TestStorageConfigService_GDriveServiceAccount(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "")

	t.Run("requires a folder id", func(t *testing.T) {
		err := svc.SaveGDriveServiceAccount(t.Context(), tenantID, service.SaveGDriveServiceAccountInput{ServiceAccountJSON: `{}`})
		assert.ErrorContains(t, err, "folderId is required")
	})

	t.Run("initial save requires a service account JSON value", func(t *testing.T) {
		err := svc.SaveGDriveServiceAccount(t.Context(), tenantID, service.SaveGDriveServiceAccountInput{FolderID: "folder-1"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, service.SaveGDriveServiceAccountInput{
		FolderID: "folder-1", ServiceAccountJSON: `{"type":"service_account"}`,
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, domain.StorageProviderGDrive, cfg.Provider)
	require.NotNil(t, cfg.GDriveAuthMethod)
	assert.Equal(t, domain.GDriveAuthMethodServiceAccount, *cfg.GDriveAuthMethod)
	assert.Equal(t, "folder-1", *cfg.GDriveFolderID)
	assert.NotContains(t, cfg.GDriveServiceAccountJSONSecretRef, "service_account", "the plaintext credentials JSON never lands in the stored ref")

	t.Run("re-saving without new credentials keeps the existing ones", func(t *testing.T) {
		require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, service.SaveGDriveServiceAccountInput{
			FolderID: "folder-2",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "folder-2", *got.GDriveFolderID)
		assert.Equal(t, cfg.GDriveServiceAccountJSONSecretRef, got.GDriveServiceAccountJSONSecretRef)
	})
}

func TestStorageConfigService_GDriveOAuth(t *testing.T) {
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("GetGDriveAuthorizeURL refuses when no Google OAuth client is configured", func(t *testing.T) {
		pool := testutil.RequireTestDB(t)
		svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "")
		_, err := svc.GetGDriveAuthorizeURL(t.Context(), tenantID, userID, "folder-1")
		assert.ErrorContains(t, err, "not configured")
	})

	t.Run("GetGDriveAuthorizeURL refuses without a folder id", func(t *testing.T) {
		svc := newStorageConfigServiceWithOAuth(t, t.TempDir())
		_, err := svc.GetGDriveAuthorizeURL(t.Context(), tenantID, userID, "")
		assert.ErrorContains(t, err, "folderId is required")
	})

	t.Run("GetGDriveAuthorizeURL returns a real Google consent URL carrying the state", func(t *testing.T) {
		svc := newStorageConfigServiceWithOAuth(t, t.TempDir())
		url, err := svc.GetGDriveAuthorizeURL(t.Context(), tenantID, userID, "folder-1")
		require.NoError(t, err)
		assert.Contains(t, url, "accounts.google.com")
		assert.Contains(t, url, "state=")
		assert.Contains(t, url, "prompt=consent")
	})

	t.Run("HandleGDriveOAuthCallback rejects an invalid/expired state before ever touching Google", func(t *testing.T) {
		svc := newStorageConfigServiceWithOAuth(t, t.TempDir())
		err := svc.HandleGDriveOAuthCallback(t.Context(), tenantID, "some-code", "not-a-real-state")
		assert.ErrorContains(t, err, "invalid or expired oauth state")
	})
}

func TestStorageConfigService_BuildStore(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "")

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

	t.Run("gcs configured but the credentials JSON doesn't build a real client", func(t *testing.T) {
		// {"type":"service_account"} (see TestStorageConfigService_GCS) is
		// structurally valid JSON but missing the fields
		// (private_key/client_email/token_uri) a real service-account
		// credential needs -- NewGCSStore fails locally parsing it, no
		// network involved, which is enough to cover BuildStore's "build
		// gcs client" error-wrap branch. The success branch needs a fully
		// valid credential and is exercised against real GCS in Fase 3, not
		// here.
		require.NoError(t, svc.SaveGCS(t.Context(), tenantID, service.SaveGCSInput{
			Bucket: "evidence", ProjectID: "argusops-prod", CredentialsJSON: `{"type":"service_account"}`,
		}))
		_, err := svc.BuildStore(t.Context(), tenantID)
		assert.ErrorContains(t, err, "build gcs client")
	})

	t.Run("gdrive service-account configured builds a *blobstore.GDriveStore", func(t *testing.T) {
		require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, service.SaveGDriveServiceAccountInput{
			FolderID: "folder-1", ServiceAccountJSON: driveTestServiceAccountJSON,
		}))
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.GDriveStore)
		assert.True(t, ok, "expected a *blobstore.GDriveStore once Google Drive (service account) is configured")
	})
}

// driveTestServiceAccountJSON is a syntactically valid, but entirely fake,
// service-account key -- enough for drive.NewService to build a local
// client object (it doesn't make a network call until an actual API
// method is invoked), same reasoning GCSStore's own test fixture would
// need if it had one.
const driveTestServiceAccountJSON = `{
	"type": "service_account",
	"project_id": "argusops-test",
	"private_key_id": "test-key-id",
	"private_key": "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n-----END PRIVATE KEY-----\n",
	"client_email": "test@argusops-test.iam.gserviceaccount.com",
	"client_id": "123456789",
	"token_uri": "https://oauth2.googleapis.com/token"
}`
