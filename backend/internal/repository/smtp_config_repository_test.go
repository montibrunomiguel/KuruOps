package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestSMTPConfigRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewSMTPConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	fromName := "ArgusOps"
	cfg := &domain.SMTPConfig{
		TenantID: tenantID, Host: "smtp.example.com", Port: 587, UseTLS: true,
		Username: "smtp-user", PasswordSecretRef: "secret://smtp-password",
		FromAddress: "no-reply@example.com", FromName: &fromName,
	}
	require.NoError(t, repo.Upsert(t.Context(), tx, cfg))

	t.Run("get after insert", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "smtp.example.com", got.Host)
		assert.Equal(t, 587, got.Port)
		assert.True(t, got.UseTLS)
		assert.Equal(t, "secret://smtp-password", got.PasswordSecretRef)
		require.NotNil(t, got.FromName)
		assert.Equal(t, fromName, *got.FromName)
	})

	t.Run("upsert replaces the row", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.SMTPConfig{
			TenantID: tenantID, Host: "smtp2.example.com", Port: 465, UseTLS: false,
			Username: "", PasswordSecretRef: "secret://smtp-password",
			FromAddress: "alerts@example.com", FromName: nil,
		}))

		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Equal(t, "smtp2.example.com", got.Host)
		assert.Equal(t, 465, got.Port)
		assert.False(t, got.UseTLS)
		assert.Nil(t, got.FromName, "switching to no display name must clear the previous one")
	})

	t.Run("delete turns email sending off", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx))
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestSMTPConfigRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewSMTPConfigRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.SMTPConfig{
		TenantID: tenantA, Host: "smtp.a.example.com", Port: 587, UseTLS: true,
		PasswordSecretRef: "secret://a", FromAddress: "a@example.com",
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.Get(t.Context(), txB)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's smtp config")
}
