package service_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// assertNoRawDriverError is the shared assertion for this file: whatever the
// message says, it must not be the Postgres driver's own wording. A response
// naming an internal constraint or table tells the caller nothing they can
// act on, and hands out schema detail for free.
func assertNoRawDriverError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	for _, leak := range []string{"SQLSTATE", "violates", "constraint", "pgx", "pq:"} {
		assert.NotContains(t, strings.ToLower(err.Error()), strings.ToLower(leak),
			"driver wording reached the caller: %v", err)
	}
}

// TestCreateDuplicatesAreTranslated covers the four admin resources whose
// duplicate-name error used to arrive as, verbatim:
//
//	duplicate key value violates unique constraint "users_tenant_email_uq" (SQLSTATE 23505)
//
// Handlers answer a service error with writeError(w, 400, err.Error()), so an
// untranslated driver error goes straight out to the API. Tags already
// translated theirs; the other four never adopted it.
func TestCreateDuplicatesAreTranslated(t *testing.T) {
	pool := testutil.RequireTestDB(t)

	t.Run("user", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		roleID := testutil.NewRole(t, tenantID, false, nil)
		svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "dup@test.local", "First", "", roleID)
		require.NoError(t, err)

		_, _, err = svc.CreateLocal(t.Context(), tenantID, actorID, "dup@test.local", "Second", "", roleID)
		assertNoRawDriverError(t, err)
		assert.ErrorContains(t, err, "dup@test.local", "the message should name the address that collided")
		assert.ErrorContains(t, err, "already exists")
	})

	t.Run("user with a role that doesn't exist", func(t *testing.T) {
		// A foreign-key violation, not a duplicate -- it used to leak
		// users_role_id_fkey the same way.
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

		missing := uuid.New()
		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "orphan@test.local", "Orphan", "", missing)
		assertNoRawDriverError(t, err)
		assert.ErrorContains(t, err, missing.String(), "the message should name the role that wasn't found")
	})

	t.Run("role", func(t *testing.T) {
		tenantID := testutil.NewTenant(t)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		svc := service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())

		in := domain.SaveRoleInput{Name: "Duplicated", ResourceAccess: domain.ResourceAccess{domain.ResourceCapabilityAlerts}}
		_, err := svc.Create(t.Context(), tenantID, actorID, in)
		require.NoError(t, err)

		_, err = svc.Create(t.Context(), tenantID, actorID, in)
		assertNoRawDriverError(t, err)
		assert.ErrorContains(t, err, "Duplicated")
		assert.ErrorContains(t, err, "already exists")
	})
}

// TestValidateEmail pins the shape check on the field that doubles as the
// login identifier and the only password-reset channel. "not-an-email", "a@"
// and "@b.com" all produced perfectly valid-looking accounts before this --
// accounts whose owner could never sign in or be reached, with the mistake
// surfacing only as "they never got the invite".
func TestValidateEmail(t *testing.T) {
	t.Run("rejects what isn't an address", func(t *testing.T) {
		for _, bad := range []string{
			"not-an-email", "a@", "@b.com", "no spaces@x.com", "two@@x.com",
			"trailing@dot.", "@", "", "user@nodot",
		} {
			assert.Error(t, domain.ValidateEmail(bad), "should have rejected %q", bad)
		}
	})

	t.Run("accepts addresses real people have", func(t *testing.T) {
		// Deliberately permissive: plus-addressing, subdomains, long TLDs and
		// dotted local parts are all legitimate, and rejecting a real address
		// is a worse failure than letting an odd one through.
		for _, ok := range []string{
			"admin@kuruops.local", "first.last@example.com", "user+tag@example.co.uk",
			"soc@mail.corp.example.com", "a@b.co", "UPPER@EXAMPLE.COM",
		} {
			assert.NoError(t, domain.ValidateEmail(ok), "should have accepted %q", ok)
		}
	})

	t.Run("the error names the value", func(t *testing.T) {
		err := domain.ValidateEmail("nope")
		require.Error(t, err)
		assert.ErrorContains(t, err, "nope")
	})
}
