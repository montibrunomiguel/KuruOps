package repository_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestWebhookRepository_InsertGetList(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewWebhookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	ep := &domain.WebhookEndpoint{
		TenantID:   tenantID,
		Name:       "Wazuh Prod",
		Source:     "wazuh",
		TokenHash:  "hash-1",
		TokenLast4: "ab12",
	}
	require.NoError(t, repo.Insert(t.Context(), tx, ep))
	require.NotEqual(t, [16]byte{}, ep.ID)
	assert.Equal(t, "active", ep.Status)

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, ep.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Wazuh Prod", got.Name)
		assert.Equal(t, "ab12", got.TokenLast4)
	})

	t.Run("list", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
	})

	t.Run("rotate token replaces hash/last4/expiry and stamps rotated_at", func(t *testing.T) {
		newExpiry := time.Now().Add(90 * 24 * time.Hour)
		require.NoError(t, repo.RotateToken(t.Context(), tx, ep.ID, "hash-2", "cd34", &newExpiry))

		got, err := repo.Get(t.Context(), tx, ep.ID)
		require.NoError(t, err)
		assert.Equal(t, "cd34", got.TokenLast4)
		assert.NotNil(t, got.RotatedAt)
		require.NotNil(t, got.ExpiresAt)
		assert.WithinDuration(t, newExpiry, *got.ExpiresAt, time.Second)
	})

	t.Run("set status", func(t *testing.T) {
		require.NoError(t, repo.SetStatus(t.Context(), tx, ep.ID, "disabled"))
		got, err := repo.Get(t.Context(), tx, ep.ID)
		require.NoError(t, err)
		assert.Equal(t, "disabled", got.Status)
	})

	t.Run("set field mapping template assigns then clears it", func(t *testing.T) {
		templateRepo := repository.NewFieldMappingTemplateRepository()
		template := &domain.FieldMappingTemplate{TenantID: tenantID, Name: "Wazuh fields"}
		require.NoError(t, templateRepo.Insert(t.Context(), tx, template))

		require.NoError(t, repo.SetFieldMappingTemplate(t.Context(), tx, ep.ID, &template.ID))
		got, err := repo.Get(t.Context(), tx, ep.ID)
		require.NoError(t, err)
		require.NotNil(t, got.FieldMappingTemplateID)
		assert.Equal(t, template.ID, *got.FieldMappingTemplateID)

		require.NoError(t, repo.SetFieldMappingTemplate(t.Context(), tx, ep.ID, nil))
		got, err = repo.Get(t.Context(), tx, ep.ID)
		require.NoError(t, err)
		assert.Nil(t, got.FieldMappingTemplateID)
	})
}

func TestWebhookRepository_ResolveToken(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewWebhookRepository()

	// ResolveToken opens its own transaction against the pool (it runs
	// before app.tenant_id can be known), so the fixture insert must be
	// committed on a separate transaction -- an uncommitted testutil.BeginTx
	// transaction would be invisible to it, unlike every other repository
	// test in this package where reads and writes share one tx. That commit
	// also means this row outlives the test (BeginTx's rollback-on-cleanup
	// doesn't apply here), so the hash must be unique per run -- a fixed
	// literal would collide with the same row from a prior test run once
	// there are two, and ResolveToken has no way to prefer one.
	tokenHash := "resolve-hash-" + tenantID.String()
	var ep domain.WebhookEndpoint
	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		ep = domain.WebhookEndpoint{
			TenantID:   tenantID,
			Name:       "CrowdStrike",
			Source:     "crowdstrike",
			TokenHash:  tokenHash,
			TokenLast4: "ef56",
		}
		return repo.Insert(t.Context(), tx, &ep)
	}))

	t.Run("resolves without app.tenant_id set, via the pool directly", func(t *testing.T) {
		got, err := repo.ResolveToken(t.Context(), pool, tokenHash)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, ep.ID, got.ID)
		assert.Equal(t, tenantID, got.TenantID)
		assert.Equal(t, "crowdstrike", got.Source)
	})

	t.Run("unknown token hash returns nil, not an error", func(t *testing.T) {
		got, err := repo.ResolveToken(t.Context(), pool, "no-such-hash")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
