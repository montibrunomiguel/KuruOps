package service_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// This test only covers the cheap, deterministic branch: no LDAP config at
// all for the tenant. See TestLDAPAuthService_Login below for the real bind
// path, exercised against testutil.FakeLDAPServer.
func TestLDAPAuthService_Login_NotConfigured(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)

	ldapSvc := service.NewLDAPAuthService(pool, repository.NewIdentityConfigRepository(), secrets.NewEnvStore(), authSvc)

	_, _, _, err := ldapSvc.Login(t.Context(), tenantID, "user@example.com", "password")
	assert.ErrorContains(t, err, "ldap is not configured")
}

// TestLDAPAuthService_Login exercises Login's full path against a real (fake)
// directory: load config, resolve the bind password secret, bind/search/bind
// via internal/authn, and hand off to AuthService.ProvisionFederated.
func TestLDAPAuthService_Login(t *testing.T) {
	pool, authSvc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	identityCfg := repository.NewIdentityConfigRepository()
	store := secrets.NewEnvStore()

	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass", GroupAttr: "memberOf",
		Users: []testutil.FakeLDAPUser{
			{
				DN: "uid=jdoe,ou=people,dc=example,dc=org", Mail: "jdoe@example.org", Password: "s3cret",
				DisplayName: "Jane Doe", Groups: []string{"cn=analysts,ou=groups,dc=example,dc=org"},
			},
		},
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	bindRef, err := store.Put(t.Context(), tenantID.String(), "ldap-bind-password", "adminpass")
	require.NoError(t, err)

	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		return identityCfg.UpsertLDAPConfig(t.Context(), tx, &domain.LDAPConfig{
			TenantID: tenantID, Host: host, Port: port, UseTLS: false,
			BindDN: "cn=admin,dc=example,dc=org", BindPasswordSecretRef: bindRef,
			UserBaseDN: "ou=people,dc=example,dc=org", UserFilter: "(mail=%s)",
			GroupBaseDN: "ou=groups,dc=example,dc=org", GroupAttribute: "memberOf",
		})
	}))

	ldapSvc := service.NewLDAPAuthService(pool, identityCfg, store, authSvc)

	t.Run("success provisions the federated user and issues tokens", func(t *testing.T) {
		user, access, refresh, err := ldapSvc.Login(t.Context(), tenantID, "jdoe@example.org", "s3cret")
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "Jane Doe", user.Name)
		assert.NotEmpty(t, access)
		assert.NotEmpty(t, refresh)
	})

	t.Run("wrong password -- ldap authentication failed", func(t *testing.T) {
		_, _, _, err := ldapSvc.Login(t.Context(), tenantID, "jdoe@example.org", "wrong-password")
		assert.ErrorContains(t, err, "ldap authentication failed")
	})

	t.Run("unresolvable bind password secret ref resolves empty, so the service-account bind itself fails", func(t *testing.T) {
		// secrets.EnvStore.Resolve never errors on an unknown ref -- it
		// returns "", nil. So an unresolvable ref surfaces one hop later,
		// as an ordinary service-account bind failure with an empty
		// password, not as "resolve ldap bind password" (that branch is
		// only reachable with a Store implementation that can actually
		// fail to resolve, e.g. VaultStore/AWSKMSStore -- out of scope for
		// this fake-directory test).
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return identityCfg.UpsertLDAPConfig(t.Context(), tx, &domain.LDAPConfig{
				TenantID: tenantID, Host: host, Port: port, UseTLS: false,
				BindDN: "cn=admin,dc=example,dc=org", BindPasswordSecretRef: "no-such-ref",
				UserBaseDN: "ou=people,dc=example,dc=org", UserFilter: "(mail=%s)",
				GroupBaseDN: "ou=groups,dc=example,dc=org", GroupAttribute: "memberOf",
			})
		}))
		_, _, _, err := ldapSvc.Login(t.Context(), tenantID, "jdoe@example.org", "s3cret")
		assert.ErrorContains(t, err, "ldap authentication failed")
	})
}
