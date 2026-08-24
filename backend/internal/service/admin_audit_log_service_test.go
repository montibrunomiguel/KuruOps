package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAdminAuditLogService_List(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), auditRepo)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), auditRepo)
	svc := service.NewAdminAuditLogService(pool, auditRepo, userSvc)

	t.Run("empty log", func(t *testing.T) {
		entries, next, err := svc.List(t.Context(), tenantID, nil, 10)
		require.NoError(t, err)
		assert.Empty(t, entries)
		assert.Nil(t, next)
	})

	// Real, committed audit events -- via TagService.Create, the same
	// InsertEvent-inside-WithTenant path every one of the ~15 feeding
	// services uses, not a raw repo insert inside an uncommitted test tx
	// (svc.List below runs its own independent pool.WithTenant call and
	// would never see rows from a tx that's never committed).
	for _, name := range []string{"phishing", "malware", "insider-threat"} {
		_, err := tagSvc.Create(t.Context(), tenantID, actorID, name, nil)
		require.NoError(t, err)
	}

	t.Run("resolves the actor's display name", func(t *testing.T) {
		entries, next, err := svc.List(t.Context(), tenantID, nil, 10)
		require.NoError(t, err)
		require.Len(t, entries, 3)
		assert.Nil(t, next, "fewer results than the page limit means no next cursor")
		for _, e := range entries {
			assert.Equal(t, "tags", e.Area)
			assert.Equal(t, "create", e.Action)
			assert.Equal(t, "Test User", e.ActorName)
		}
	})

	t.Run("paginates with a next cursor when a page is full", func(t *testing.T) {
		page, next, err := svc.List(t.Context(), tenantID, nil, 2)
		require.NoError(t, err)
		require.Len(t, page, 2)
		require.NotNil(t, next, "a full page must carry a next cursor")

		rest, next2, err := svc.List(t.Context(), tenantID, next, 2)
		require.NoError(t, err)
		require.Len(t, rest, 1)
		assert.Nil(t, next2)
	})

	t.Run("deactivated actor still resolves a name from before deactivation", func(t *testing.T) {
		require.NoError(t, userSvc.SetActive(t.Context(), tenantID, actorID, actorID, false))
		entries, _, err := svc.List(t.Context(), tenantID, nil, 10)
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		assert.Equal(t, "Test User", entries[0].ActorName, "ListSummaries excludes inactive users, but List (used here) must not")
	})
}
