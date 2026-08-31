package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubResourceRoutesRejectTagRestrictedCaller is the structural guard
// against the tag-scoping gap re-appearing on a future endpoint.
//
// Tag visibility (domain.Role.AllowedTags) used to be enforced only on the
// "main" alert/incident routes -- Get, ChangeStatus, UpdateTags and so on --
// while every sub-resource route (comments, IOCs, timeline, status history,
// linked alerts, MCP tool-call approve/reject) queried by ID under tenant
// RLS alone. That was directly exploitable: nothing forces an HTTP client
// to call GET /incidents/{id} before GET /incidents/{id}/comments, so a
// tag-restricted analyst who knew an out-of-scope ID could read its Team
// Notes and IOCs, write new ones, and approve its side-effecting MCP tool
// calls.
//
// Rather than pin a hand-written list of routes (which is exactly what
// silently went stale last time -- each new sub-resource was added without
// anyone re-deciding about tags), this walks the *actually registered*
// chi routes and asserts that no {id}-scoped route ever answers a
// tag-restricted caller with a 2xx. A newly added sub-resource is therefore
// covered the moment it's registered, with no test change required; if it
// forgets its gate, this test fails.
//
// The assertion is deliberately "never 2xx" rather than "always exactly
// 404": a gated handler that rejects earlier for its own reasons (400 on an
// undecodable body, 405, ...) is still refusing the request, and pinning an
// exact status would make this test brittle against unrelated handler
// changes without making the security property any stronger.
func TestSubResourceRoutesRejectTagRestrictedCaller(t *testing.T) {
	t.Run("incidents", func(t *testing.T) {
		h, tenantID, actorID, incidentID := newIncidentHandlerFixture(t)
		assertNoRouteAllows(t, h.Routes, tenantID, actorID, incidentID)
	})

	t.Run("alerts", func(t *testing.T) {
		h, tenantID, actorID, alertID := newAlertHandlerFixture(t)
		assertNoRouteAllows(t, h.Routes, tenantID, actorID, alertID)
	})
}

// routesExemptFromTagScoping are the registered patterns that legitimately
// have no existing entity to tag-check, so they're expected to succeed for
// any authenticated caller. Every other route must refuse.
//
//   - "/" (GET list, POST create): a list is filtered per-caller by the
//     repository, and a create has no prior entity to check.
//   - "/bulk/*": operates on an ID list in the request body, and gates each
//     one individually inside the service (see BulkChangeStatus /
//     BulkChangePhase) rather than via a URL param this test can substitute.
var routesExemptFromTagScoping = map[string]bool{
	"/":            true,
	"/bulk/status": true,
	"/bulk/phase":  true,
}

func assertNoRouteAllows(t *testing.T, mount func(chi.Router), tenantID, actorID, entityID uuid.UUID) {
	t.Helper()
	r := newRouter(mount)

	// A caller whose role is scoped to one tag. The fixture entity carries
	// no tags at all, so tagsVisible() denies it (a non-empty AllowedTags
	// requires an actual overlap) -- see service.tagsVisible.
	restricted := []string{"some-other-team"}

	walked := 0
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if routesExemptFromTagScoping[route] {
			return nil
		}
		if !strings.Contains(route, "{id}") {
			return nil
		}
		walked++

		// Substitute every URL param: {id} is the entity under test, every
		// other param gets a well-formed placeholder so the request reaches
		// the handler's authorization logic rather than failing to parse.
		path := strings.ReplaceAll(route, "{id}", entityID.String())
		for _, p := range []string{"{alertId}", "{otherId}"} {
			path = strings.ReplaceAll(path, p, uuid.New().String())
		}
		path = strings.ReplaceAll(path, "{callId}", "1")
		path = strings.ReplaceAll(path, "{phase}", "containment")
		path = strings.ReplaceAll(path, "{role}", "commander")

		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := doRequest(r, withClaims(req, tenantID, actorID, restricted))

		assert.Falsef(t, rec.Code >= 200 && rec.Code < 300,
			"%s %s answered %d for a caller whose allowedTags don't match the entity -- "+
				"every {id}-scoped route must re-check tag visibility (see this test's doc comment); "+
				"if this route genuinely needs no entity check, add it to routesExemptFromTagScoping "+
				"with a comment saying why", method, route, rec.Code)
		return nil
	})
	require.NoError(t, err)
	require.NotZerof(t, walked, "chi.Walk found no {id}-scoped routes -- the walk itself is broken, not the routes")
	t.Logf("checked %d {id}-scoped routes", walked)
}
