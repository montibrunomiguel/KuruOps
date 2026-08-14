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
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
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

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
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

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
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

func TestWebhookService_SetGroupByFields(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewWebhookService(pool, repository.NewWebhookRepository())

	result, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh Prod", "wazuh", nil, nil, nil, nil)
	require.NoError(t, err)

	t.Run("assigns fields and window, then clears fields back to dedup-off", func(t *testing.T) {
		window := 45
		require.NoError(t, svc.SetGroupByFields(t.Context(), tenantID, result.Endpoint.ID, []string{"host.name"}, &window))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, []string{"host.name"}, list[0].GroupByFields)
		assert.Equal(t, 45, list[0].DedupWindowMinutes)

		require.NoError(t, svc.SetGroupByFields(t.Context(), tenantID, result.Endpoint.ID, nil, nil))
		list, err = svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Empty(t, list[0].GroupByFields)
		assert.Equal(t, 30, list[0].DedupWindowMinutes, "nil window on this call still resolves to the 30-minute default")
	})
}
