package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestFieldMappingTemplateService_CreateListGetUpdateDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), auditRepo)

	t.Run("rejects a blank name", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, "  ", nil)
		assert.ErrorContains(t, err, "template name is required")
	})

	t.Run("drops rules with a blank path or label", func(t *testing.T) {
		template, err := svc.Create(t.Context(), tenantID, actorID, "Wazuh fields", []domain.FieldMappingRule{
			{JSONPath: "rule.level", Label: "Rule Level"},
			{JSONPath: "", Label: "dropped"},
			{JSONPath: "dropped", Label: ""},
			{JSONPath: "  ", Label: "  "},
		})
		require.NoError(t, err)
		require.Len(t, template.Rules, 1)
		assert.Equal(t, "rule.level", template.Rules[0].JSONPath)

		t.Run("get returns it", func(t *testing.T) {
			got, err := svc.Get(t.Context(), tenantID, template.ID)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "Wazuh fields", got.Name)
		})

		t.Run("list includes it", func(t *testing.T) {
			list, err := svc.List(t.Context(), tenantID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			assert.Equal(t, template.ID, list[0].ID)
		})

		t.Run("update replaces name and rules, dropping blanks the same way", func(t *testing.T) {
			updated, err := svc.Update(t.Context(), tenantID, actorID, template.ID, "Wazuh fields v2", []domain.FieldMappingRule{
				{JSONPath: "rule.groups", Label: "Categories"},
				{JSONPath: "", Label: "dropped"},
			})
			require.NoError(t, err)
			assert.Equal(t, "Wazuh fields v2", updated.Name)
			require.Len(t, updated.Rules, 1)
			assert.Equal(t, "Categories", updated.Rules[0].Label)
		})

		t.Run("update rejects a blank name", func(t *testing.T) {
			_, err := svc.Update(t.Context(), tenantID, actorID, template.ID, "", nil)
			assert.ErrorContains(t, err, "template name is required")
		})

		t.Run("delete removes it", func(t *testing.T) {
			require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, template.ID))
			got, err := svc.Get(t.Context(), tenantID, template.ID)
			require.NoError(t, err)
			assert.Nil(t, got)
		})

		t.Run("create/update/delete each record an admin audit event", func(t *testing.T) {
			tx := testutil.BeginTx(t, pool, tenantID)
			events, err := auditRepo.List(t.Context(), tx, nil, 10)
			require.NoError(t, err)
			var actions []string
			for _, e := range events {
				assert.Equal(t, "field-mapping-templates", e.Area)
				actions = append(actions, e.Action)
			}
			assert.Contains(t, actions, "create")
			assert.Contains(t, actions, "update")
			assert.Contains(t, actions, "delete")
		})
	})
}

func TestFieldMappingTemplateService_Get_UnknownID(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())

	got, err := svc.Get(t.Context(), tenantID, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)
}
