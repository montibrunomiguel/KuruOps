package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// Login's actual LDAP bind is not exercised here -- it needs a live
// directory server, the same boundary already applied to
// internal/authn.AuthenticateLDAP (see authn/ldap_test.go's absence and the
// authn package's documented coverage gap). This test only covers the cheap,
// deterministic branch: no LDAP config at all for the tenant.
func TestLDAPAuthService_Login_NotConfigured(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)

	ldapSvc := service.NewLDAPAuthService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), authSvc)

	_, _, _, err := ldapSvc.Login(t.Context(), tenantID, "user@example.com", "password")
	assert.ErrorContains(t, err, "ldap is not configured")
}
