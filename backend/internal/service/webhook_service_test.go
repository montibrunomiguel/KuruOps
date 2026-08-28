package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeWebhookRepo lets a test fail a specific repo call on demand --
// WebhookService takes an interface (not the concrete
// *repository.WebhookRepository) specifically so this is possible. See
// fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeWebhookRepo struct {
	insertErr                  error
	rotateTokenErr             error
	setStatusErr               error
	setFieldMappingTemplateErr error
	setGroupByFieldsErr        error
}

func (f *fakeWebhookRepo) List(context.Context, pgx.Tx) ([]domain.WebhookEndpoint, error) {
	return nil, nil
}
func (f *fakeWebhookRepo) Insert(context.Context, pgx.Tx, *domain.WebhookEndpoint) error {
	return f.insertErr
}
func (f *fakeWebhookRepo) RotateToken(context.Context, pgx.Tx, uuid.UUID, string, string, *time.Time) error {
	return f.rotateTokenErr
}
func (f *fakeWebhookRepo) SetStatus(context.Context, pgx.Tx, uuid.UUID, string) error {
	return f.setStatusErr
}
func (f *fakeWebhookRepo) SetFieldMappingTemplate(context.Context, pgx.Tx, uuid.UUID, *uuid.UUID) error {
	return f.setFieldMappingTemplateErr
}
func (f *fakeWebhookRepo) SetGroupByFields(context.Context, pgx.Tx, uuid.UUID, []string, int) error {
	return f.setGroupByFieldsErr
}

func TestWebhookService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository(), repository.NewAdminAuditEventRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Token)
	assert.NotNil(t, result.Endpoint.ExpiresAt, "no explicit expiry defaults to the 90-day policy")
	assert.Equal(t, "active", result.Endpoint.Status)
	assert.Empty(t, result.Endpoint.GroupByFields, "no group-by fields configured means dedup is off")
	assert.Equal(t, 30, result.Endpoint.DedupWindowMinutes, "no explicit window defaults to 30 minutes")

	t.Run("expiresInDays=0 opts the endpoint out of expiring", func(t *testing.T) {
		zero := 0
		result, err := svc.Create(t.Context(), tenantID, actorID, "Never Expires", "custom", &zero, nil, nil, nil)
		require.NoError(t, err)
		assert.Nil(t, result.Endpoint.ExpiresAt)
	})

	t.Run("list shows both endpoints without ever exposing the plaintext token", func(t *testing.T) {
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("explicit group-by fields and window are persisted as given", func(t *testing.T) {
		window := 45
		result, err := svc.Create(t.Context(), tenantID, actorID, "Custom Window", "custom", nil, nil, []string{"host.name"}, &window)
		require.NoError(t, err)
		assert.Equal(t, []string{"host.name"}, result.Endpoint.GroupByFields)
		assert.Equal(t, 45, result.Endpoint.DedupWindowMinutes)
	})
}

func TestWebhookService_Regenerate(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository(), repository.NewAdminAuditEventRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)

	newToken, err := svc.Regenerate(t.Context(), tenantID, actorID, result.Endpoint.ID, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, newToken)
	assert.NotEqual(t, result.Token, newToken)
}

func TestWebhookService_SetStatus(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository(), repository.NewAdminAuditEventRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)

	t.Run("rejects an invalid status", func(t *testing.T) {
		err := svc.SetStatus(t.Context(), tenantID, actorID, result.Endpoint.ID, "bogus")
		assert.ErrorContains(t, err, "invalid status")
	})

	t.Run("disabled is accepted", func(t *testing.T) {
		require.NoError(t, svc.SetStatus(t.Context(), tenantID, actorID, result.Endpoint.ID, "disabled"))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "disabled", list[0].Status)
	})
}

func TestWebhookService_SetFieldMappingTemplate(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository(), repository.NewAdminAuditEventRepository())
	templateSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)
	template, err := templateSvc.Create(t.Context(), tenantID, actorID, "Wazuh fields", nil)
	require.NoError(t, err)

	t.Run("assigns then clears the template", func(t *testing.T) {
		require.NoError(t, svc.SetFieldMappingTemplate(t.Context(), tenantID, actorID, result.Endpoint.ID, &template.ID))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.NotNil(t, list[0].FieldMappingTemplateID)
		assert.Equal(t, template.ID, *list[0].FieldMappingTemplateID)

		require.NoError(t, svc.SetFieldMappingTemplate(t.Context(), tenantID, actorID, result.Endpoint.ID, nil))
		list, err = svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, list[0].FieldMappingTemplateID)
	})
}

func TestWebhookService_SetGroupByFields(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository(), auditRepo)

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)

	t.Run("assigns fields and window, then clears fields back to dedup-off", func(t *testing.T) {
		window := 45
		require.NoError(t, svc.SetGroupByFields(t.Context(), tenantID, actorID, result.Endpoint.ID, []string{"host.name"}, &window))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, []string{"host.name"}, list[0].GroupByFields)
		assert.Equal(t, 45, list[0].DedupWindowMinutes)

		require.NoError(t, svc.SetGroupByFields(t.Context(), tenantID, actorID, result.Endpoint.ID, nil, nil))
		list, err = svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Empty(t, list[0].GroupByFields)
		assert.Equal(t, 30, list[0].DedupWindowMinutes, "nil window on this call still resolves to the 30-minute default")
	})

	t.Run("create and set-group-by-fields each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "webhooks", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "create")
		assert.Contains(t, actions, "set-group-by-fields")
	})
}

// TestWebhookService_RepoErrors exercises each mutating method's
// error-wrapping branch around the repo call that persists -- unreachable
// via a real Postgres integration test.
func TestWebhookService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("Create wraps an Insert failure", func(t *testing.T) {
		svc := service.NewWebhookService(pool, &fakeWebhookRepo{insertErr: errors.New("insert boom")}, repository.NewAdminAuditEventRepository())
		_, err := svc.Create(t.Context(), tenantID, actorID, "X", "wazuh", nil, nil, nil, nil)
		assert.ErrorContains(t, err, "insert boom")
	})

	t.Run("Regenerate wraps a RotateToken failure", func(t *testing.T) {
		svc := service.NewWebhookService(pool, &fakeWebhookRepo{rotateTokenErr: errors.New("rotate boom")}, repository.NewAdminAuditEventRepository())
		_, err := svc.Regenerate(t.Context(), tenantID, actorID, uuid.New(), nil)
		assert.ErrorContains(t, err, "rotate boom")
	})

	t.Run("SetStatus wraps a SetStatus failure", func(t *testing.T) {
		svc := service.NewWebhookService(pool, &fakeWebhookRepo{setStatusErr: errors.New("set-status boom")}, repository.NewAdminAuditEventRepository())
		err := svc.SetStatus(t.Context(), tenantID, actorID, uuid.New(), "active")
		assert.ErrorContains(t, err, "set-status boom")
	})

	t.Run("SetFieldMappingTemplate wraps a SetFieldMappingTemplate failure", func(t *testing.T) {
		svc := service.NewWebhookService(pool, &fakeWebhookRepo{setFieldMappingTemplateErr: errors.New("set-fmt boom")}, repository.NewAdminAuditEventRepository())
		err := svc.SetFieldMappingTemplate(t.Context(), tenantID, actorID, uuid.New(), nil)
		assert.ErrorContains(t, err, "set-fmt boom")
	})

	t.Run("SetGroupByFields wraps a SetGroupByFields failure", func(t *testing.T) {
		svc := service.NewWebhookService(pool, &fakeWebhookRepo{setGroupByFieldsErr: errors.New("set-gbf boom")}, repository.NewAdminAuditEventRepository())
		err := svc.SetGroupByFields(t.Context(), tenantID, actorID, uuid.New(), []string{"host.name"}, nil)
		assert.ErrorContains(t, err, "set-gbf boom")
	})
}
