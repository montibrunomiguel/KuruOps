package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/blobstore"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeStorageConfigRepo lets a test fail a specific repo call on demand --
// StorageConfigService takes an interface (not the concrete
// *repository.StorageConfigRepository) specifically so this is possible.
// Every mutating method fetches the existing config first to build the
// audit-diff, then upserts/deletes; a real Postgres integration test has no
// way to make either of those two calls fail mid-transaction, so those
// error-wrapping branches would otherwise never run.
type fakeStorageConfigRepo struct {
	getErr    error
	upsertErr error
	deleteErr error
}

func (f *fakeStorageConfigRepo) Get(context.Context, pgx.Tx) (*domain.StorageConfig, error) {
	return nil, f.getErr
}
func (f *fakeStorageConfigRepo) Upsert(context.Context, pgx.Tx, *domain.StorageConfig) error {
	return f.upsertErr
}
func (f *fakeStorageConfigRepo) Delete(context.Context, pgx.Tx) error { return f.deleteErr }

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
		oauthStates, "test-client-id", "test-client-secret", "https://kuruops.example/auth/oauth/gdrive/callback", repository.NewAdminAuditEventRepository())
}

func TestStorageConfigService_S3(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	dir := t.TempDir()
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "", auditRepo)

	t.Run("initial save requires a secret access key", func(t *testing.T) {
		err := svc.SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{Bucket: "b", Region: "us-east-1", AccessKeyID: "AKIA"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{
		Bucket: "evidence", Region: "us-east-1", AccessKeyID: "AKIA", SecretAccessKey: "s3cret",
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotContains(t, cfg.S3SecretAccessKeySecretRef, "s3cret", "the plaintext secret key never lands in the stored ref")

	t.Run("re-saving without a new secret keeps the existing one", func(t *testing.T) {
		require.NoError(t, svc.SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{
			Bucket: "evidence-renamed", Region: "us-east-1", AccessKeyID: "AKIA",
		}))
		got, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, "evidence-renamed", *got.S3Bucket)
		assert.Equal(t, cfg.S3SecretAccessKeySecretRef, got.S3SecretAccessKeySecretRef)
	})

	t.Run("each successful save records an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		require.Len(t, events, 2)
		for _, e := range events {
			assert.Equal(t, "storage-config", e.Area)
			assert.Equal(t, "save-s3", e.Action)
		}
	})
}

func TestStorageConfigService_GCS(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "", repository.NewAdminAuditEventRepository())

	t.Run("initial save requires credentials JSON", func(t *testing.T) {
		err := svc.SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{Bucket: "b", ProjectID: "p"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{
		Bucket: "evidence", ProjectID: "kuruops-prod", CredentialsJSON: `{"type":"service_account"}`,
	}))

	cfg, err := svc.Get(t.Context(), tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "kuruops-prod", *cfg.GCSProjectID)
	assert.NotContains(t, cfg.GCSCredentialsJSONSecretRef, "service_account", "the plaintext credentials JSON never lands in the stored ref")

	t.Run("re-saving without new credentials keeps the existing ones", func(t *testing.T) {
		require.NoError(t, svc.SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{
			Bucket: "evidence-renamed", ProjectID: "kuruops-prod",
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
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "", repository.NewAdminAuditEventRepository())

	t.Run("requires a folder id", func(t *testing.T) {
		err := svc.SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{ServiceAccountJSON: `{}`})
		assert.ErrorContains(t, err, "folderId is required")
	})

	t.Run("initial save requires a service account JSON value", func(t *testing.T) {
		err := svc.SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{FolderID: "folder-1"})
		assert.ErrorContains(t, err, "credential value is required")
	})

	require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
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
		require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
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
		svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
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
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	dir := t.TempDir()
	svc := service.NewStorageConfigService(pool, repository.NewStorageConfigRepository(), secrets.NewEnvStore(), dir, nil, "", "", "", repository.NewAdminAuditEventRepository())

	t.Run("no config configured falls back to local disk", func(t *testing.T) {
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.LocalStore)
		assert.True(t, ok, "expected a *blobstore.LocalStore fallback")
	})

	t.Run("s3 configured builds an S3Store", func(t *testing.T) {
		require.NoError(t, svc.SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{
			Bucket: "evidence", Region: "us-east-1", AccessKeyID: "AKIA", SecretAccessKey: "s3cret",
		}))
		store, err := svc.BuildStore(t.Context(), tenantID)
		require.NoError(t, err)
		_, ok := store.(*blobstore.S3Store)
		assert.True(t, ok, "expected a *blobstore.S3Store once S3 is configured")
	})

	t.Run("deleting the config reverts to local disk", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, actorID))
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
		require.NoError(t, svc.SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{
			Bucket: "evidence", ProjectID: "kuruops-prod", CredentialsJSON: `{"type":"service_account"}`,
		}))
		_, err := svc.BuildStore(t.Context(), tenantID)
		assert.ErrorContains(t, err, "build gcs client")
	})

	t.Run("gdrive service-account configured builds a *blobstore.GDriveStore", func(t *testing.T) {
		require.NoError(t, svc.SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
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
	"project_id": "kuruops-test",
	"private_key_id": "test-key-id",
	"private_key": "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n-----END PRIVATE KEY-----\n",
	"client_email": "test@kuruops-test.iam.gserviceaccount.com",
	"client_id": "123456789",
	"token_uri": "https://oauth2.googleapis.com/token"
}`

// TestStorageConfigService_RepoErrors exercises the "load existing config to
// build the audit diff" and "persist" error-wrapping branches every
// mutating method has -- unreachable via a real Postgres integration test,
// since nothing in these tests can make an otherwise-healthy query fail
// mid-transaction. See fakeStorageConfigRepo's doc comment.
func TestStorageConfigService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	newSvc := func(fake *fakeStorageConfigRepo) *service.StorageConfigService {
		return service.NewStorageConfigService(pool, fake, secrets.NewEnvStore(), t.TempDir(), nil, "", "", "", repository.NewAdminAuditEventRepository())
	}

	t.Run("Delete wraps a Get failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{getErr: errors.New("get boom")}).Delete(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{deleteErr: errors.New("delete boom")}).Delete(t.Context(), tenantID, actorID)
		assert.ErrorContains(t, err, "delete boom")
	})

	t.Run("SaveS3 wraps a Get failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{getErr: errors.New("get boom")}).SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{
			Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s",
		})
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("SaveS3 wraps an Upsert failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{upsertErr: errors.New("upsert boom")}).SaveS3(t.Context(), tenantID, actorID, service.SaveS3Input{
			Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s",
		})
		assert.ErrorContains(t, err, "upsert boom")
	})

	t.Run("SaveGCS wraps a Get failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{getErr: errors.New("get boom")}).SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{
			Bucket: "b", ProjectID: "p", CredentialsJSON: "{}",
		})
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("SaveGCS wraps an Upsert failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{upsertErr: errors.New("upsert boom")}).SaveGCS(t.Context(), tenantID, actorID, service.SaveGCSInput{
			Bucket: "b", ProjectID: "p", CredentialsJSON: "{}",
		})
		assert.ErrorContains(t, err, "upsert boom")
	})

	t.Run("SaveGDriveServiceAccount wraps a Get failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{getErr: errors.New("get boom")}).SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
			FolderID: "f", ServiceAccountJSON: "{}",
		})
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("SaveGDriveServiceAccount wraps an Upsert failure", func(t *testing.T) {
		err := newSvc(&fakeStorageConfigRepo{upsertErr: errors.New("upsert boom")}).SaveGDriveServiceAccount(t.Context(), tenantID, actorID, service.SaveGDriveServiceAccountInput{
			FolderID: "f", ServiceAccountJSON: "{}",
		})
		assert.ErrorContains(t, err, "upsert boom")
	})
}
