package repository_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestListEndpointsNeverSerializeNilSlices covers the nil-slice-to-JSON-null
// trap this codebase has now hit three times (see the Assignees normalizer in
// IncidentRepository, and the map-lookup variant documented alongside it).
//
// The shape is always the same and always invisible to the type checker: a
// []T field that a list query doesn't populate stays nil, marshals to null,
// and the TypeScript side declares it non-nullable -- so nothing warns until
// a component calls .filter/.find on it and dies. Both fields below are
// genuinely not loaded by their list query, which is a deliberate cost
// decision; what must not leak out is the null.
//
// assert.NotNil is the point here, not assert.Empty -- Empty passes for nil
// too, which is exactly why the earlier occurrences went unnoticed.
func TestListEndpointsNeverSerializeNilSlices(t *testing.T) {
	pool := testutil.RequireTestDB(t)

	t.Run("incident list carries roles as [] not null", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		repo := repository.NewIncidentRepository()

		inc := &domain.Incident{
			TenantID: tenantID, Title: "QA nil roles", Severity: domain.SeverityHigh,
			Priority: domain.PriorityP2, Phase: domain.PhaseDetectionAnalysis,
			Tags: []string{},
		}
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return repo.Insert(t.Context(), tx, inc)
		}))

		var listed []domain.Incident
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			v, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{})
			listed = v
			return err
		}))
		require.NotEmpty(t, listed)

		for _, got := range listed {
			assert.NotNil(t, got.Roles, "Roles must never be nil -- it marshals to null")
		}

		encoded, err := json.Marshal(listed[0])
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"roles":[]`)
		assert.NotContains(t, string(encoded), `"roles":null`)
	})

	t.Run("on-call schedule list carries overrides as [] not null", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		repo := repository.NewOnCallScheduleRepository()

		sched := &domain.OnCallSchedule{
			TenantID: tenantID, Name: "QA nil overrides", PeriodDays: 7,
			ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		}
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return repo.Insert(t.Context(), tx, sched)
		}))

		var listed []domain.OnCallSchedule
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			v, err := repo.List(t.Context(), tx)
			listed = v
			return err
		}))
		require.NotEmpty(t, listed)

		for _, got := range listed {
			assert.NotNil(t, got.Overrides, "Overrides must never be nil -- OnCallTimeline calls .find on it")
		}

		encoded, err := json.Marshal(listed[0])
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), `"overrides":null`)
	})
}
