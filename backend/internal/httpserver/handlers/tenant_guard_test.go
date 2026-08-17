package handlers_test

import (
	"testing"
)

// TestHandlers_AllRoutesRequireTenant is the coverage/regression companion
// to mustTenantID's own introduction (respond.go): the audit that added it
// found several mutation handlers skipped the tenant check entirely, and
// fixed that by calling mustTenantID at the top of every handler method,
// not just the read ones a couple of pre-existing *_MissingTenantContext
// tests already covered. This walks every route each handler registers and
// confirms the guard actually fires for all of them, not just the ones
// that happened to have their own dedicated test already.
func TestHandlers_AllRoutesRequireTenant(t *testing.T) {
	t.Run("AlertHandlers", func(t *testing.T) {
		h, _, _, _ := newAlertHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("IncidentHandlers", func(t *testing.T) {
		h, _, _, _ := newIncidentHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("RoleHandlers", func(t *testing.T) {
		h, _ := newRoleHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("WebhookHandlers", func(t *testing.T) {
		h, _, _ := newWebhookHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("PlaybookHandlers", func(t *testing.T) {
		h, _, _ := newPlaybookHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("UserHandlers", func(t *testing.T) {
		h, _, _ := newUserHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("OnCallScheduleHandlers", func(t *testing.T) {
		h, _, _ := newOnCallScheduleHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("LLMProviderHandlers", func(t *testing.T) {
		h, _, _ := newLLMProviderHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("MCPServerHandlers", func(t *testing.T) {
		h, _, _ := newMCPServerHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("TagHandlers", func(t *testing.T) {
		h, _, _ := newTagHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("FieldMappingTemplateHandlers", func(t *testing.T) {
		h, _, _ := newFieldMappingTemplateHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
	t.Run("EscalationPolicyHandlers", func(t *testing.T) {
		h, _, _ := newEscalationPolicyHandlerFixture(t)
		assertAllRoutesRequireTenant(t, h.Routes)
	})
}
