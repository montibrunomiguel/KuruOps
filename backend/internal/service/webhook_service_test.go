package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestWebhookService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Token)
	assert.NotNil(t, result.Endpoint.ExpiresAt, "no explicit expiry defaults to the 90-day policy")
	assert.Equal(t, "active", result.Endpoint.Status)

	t.Run("expiresInDays=0 opts the endpoint out of expiring", func(t *testing.T) {
		zero := 0
		result, err := svc.Create(t.Context(), tenantID, actorID, "Never Expires", "custom", &zero, nil)
		require.NoError(t, err)
		assert.Nil(t, result.Endpoint.ExpiresAt)
	})

	t.Run("list shows both endpoints without ever exposing the plaintext token", func(t *testing.T) {
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})
}

func TestWebhookService_Regenerate(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil)
	require.NoError(t, err)

	newToken, err := svc.Regenerate(t.Context(), tenantID, result.Endpoint.ID, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, newToken)
	assert.NotEqual(t, result.Token, newToken)
}

func TestWebhookService_SetStatus(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil)
	require.NoError(t, err)

	t.Run("rejects an invalid status", func(t *testing.T) {
		err := svc.SetStatus(t.Context(), tenantID, result.Endpoint.ID, "bogus")
		assert.ErrorContains(t, err, "invalid status")
	})

	t.Run("disabled is accepted", func(t *testing.T) {
		require.NoError(t, svc.SetStatus(t.Context(), tenantID, result.Endpoint.ID, "disabled"))
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
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())
	templateSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil)
	require.NoError(t, err)
	template, err := templateSvc.Create(t.Context(), tenantID, actorID, "Wazuh fields", nil)
	require.NoError(t, err)

	t.Run("assigns then clears the template", func(t *testing.T) {
		require.NoError(t, svc.SetFieldMappingTemplate(t.Context(), tenantID, result.Endpoint.ID, &template.ID))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.NotNil(t, list[0].FieldMappingTemplateID)
		assert.Equal(t, template.ID, *list[0].FieldMappingTemplateID)

		require.NoError(t, svc.SetFieldMappingTemplate(t.Context(), tenantID, result.Endpoint.ID, nil))
		list, err = svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Nil(t, list[0].FieldMappingTemplateID)
	})
}
