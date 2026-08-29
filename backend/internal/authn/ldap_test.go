package authn_test

// See testutil.FakeLDAPServer for the fake directory this test authenticates
// against.

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/testutil"
)

func testParams(host string, port int) authn.LDAPParams {
	return authn.LDAPParams{
		Host:           host,
		Port:           port,
		BindDN:         "cn=admin,dc=example,dc=org",
		BindPassword:   "adminpass",
		UserBaseDN:     "ou=people,dc=example,dc=org",
		UserFilter:     "(mail=%s)",
		GroupBaseDN:    "ou=groups,dc=example,dc=org",
		GroupAttribute: "memberOf",
	}
}

func TestAuthenticateLDAP_Success(t *testing.T) {
	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass", GroupAttr: "memberOf",
		Users: []testutil.FakeLDAPUser{
			{
				DN: "uid=jdoe,ou=people,dc=example,dc=org", Mail: "jdoe@example.org", Password: "s3cret",
				DisplayName: "Jane Doe", CN: "jdoe", Groups: []string{"cn=analysts,ou=groups,dc=example,dc=org"},
			},
		},
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	result, err := authn.AuthenticateLDAP(testParams(host, port), "jdoe@example.org", "s3cret")
	require.NoError(t, err)
	assert.Equal(t, "uid=jdoe,ou=people,dc=example,dc=org", result.DN)
	assert.Equal(t, "Jane Doe", result.Name)
	assert.Equal(t, []string{"cn=analysts,ou=groups,dc=example,dc=org"}, result.Groups)
}

func TestAuthenticateLDAP_NameFallback(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		cn          string
		wantName    string
	}{
		{"uses displayName when set", "Jane Doe", "jdoe", "Jane Doe"},
		{"falls back to cn when displayName is empty", "", "jdoe", "jdoe"},
		{"falls back to email when both are empty", "", "", "noname@example.org"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &testutil.FakeLDAPServer{
				BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass", GroupAttr: "memberOf",
				Users: []testutil.FakeLDAPUser{
					{
						DN: "uid=u,ou=people,dc=example,dc=org", Mail: "noname@example.org", Password: "s3cret",
						DisplayName: tc.displayName, CN: tc.cn,
					},
				},
			}
			host, port := testutil.StartFakeLDAPServer(t, srv)

			result, err := authn.AuthenticateLDAP(testParams(host, port), "noname@example.org", "s3cret")
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, result.Name)
		})
	}
}

func TestAuthenticateLDAP_ServiceAccountBindFails(t *testing.T) {
	srv := &testutil.FakeLDAPServer{BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass"}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	params := testParams(host, port)
	params.BindPassword = "wrong-password"

	_, err := authn.AuthenticateLDAP(params, "jdoe@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service account bind failed")
}

func TestAuthenticateLDAP_SearchError(t *testing.T) {
	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass",
		SearchResultCode: testutil.LDAPResultOperationsError,
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	_, err := authn.AuthenticateLDAP(testParams(host, port), "jdoe@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search for user")
}

func TestAuthenticateLDAP_UserNotFound(t *testing.T) {
	srv := &testutil.FakeLDAPServer{BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass"}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	_, err := authn.AuthenticateLDAP(testParams(host, port), "nobody@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not found or ambiguous match")
}

func TestAuthenticateLDAP_AmbiguousMatch(t *testing.T) {
	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass",
		Users: []testutil.FakeLDAPUser{
			{DN: "uid=a,ou=people,dc=example,dc=org", Mail: "dup@example.org", Password: "s3cret"},
			{DN: "uid=b,ou=people,dc=example,dc=org", Mail: "dup@example.org", Password: "other"},
		},
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	_, err := authn.AuthenticateLDAP(testParams(host, port), "dup@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not found or ambiguous match")
}

func TestAuthenticateLDAP_InvalidUserCredentials(t *testing.T) {
	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass",
		Users: []testutil.FakeLDAPUser{
			{DN: "uid=jdoe,ou=people,dc=example,dc=org", Mail: "jdoe@example.org", Password: "s3cret"},
		},
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	_, err := authn.AuthenticateLDAP(testParams(host, port), "jdoe@example.org", "wrong-password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid credentials")
}

func TestAuthenticateLDAP_EmptyPasswordRejectedBeforeDialing(t *testing.T) {
	// RFC 4513 §5.1.2: a bind with a valid DN and a zero-length password is
	// an "unauthenticated bind" that many directories treat as successful
	// without checking anything -- so this must be rejected by
	// AuthenticateLDAP itself, before ever reaching the server. Proven here
	// by pointing at a port nothing listens on: if the empty-password check
	// didn't run first, this would fail with "connect to ldap" instead.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().(*net.TCPAddr)
	require.NoError(t, ln.Close())

	for _, password := range []string{"", "   "} {
		_, err := authn.AuthenticateLDAP(testParams("127.0.0.1", addr.Port), "jdoe@example.org", password)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid credentials")
		assert.NotContains(t, err.Error(), "connect to ldap")
	}
}

func TestAuthenticateLDAP_DialFailure(t *testing.T) {
	// Nothing listens on this port (we opened and immediately closed it) --
	// connection refused, exercising the "connect to ldap" error branch of
	// AuthenticateLDAP without waiting out ldapDialTimeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().(*net.TCPAddr)
	require.NoError(t, ln.Close())

	_, err = authn.AuthenticateLDAP(testParams("127.0.0.1", addr.Port), "jdoe@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect to ldap")
}

func TestAuthenticateLDAP_TLSHandshakeFailure(t *testing.T) {
	// The fake server only ever speaks plaintext. Dialing it with UseTLS
	// exercises dialLDAP's TLS-enabled branch (ldaps:// DialURL) and proves
	// it fails closed on a server that won't speak TLS back, rather than
	// silently falling through to plaintext.
	srv := &testutil.FakeLDAPServer{BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass"}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	params := testParams(host, port)
	params.UseTLS = true

	_, err := authn.AuthenticateLDAP(params, "jdoe@example.org", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect to ldap")
}

func TestAuthenticateLDAP_FilterInjectionIsEscaped(t *testing.T) {
	// A crafted email containing LDAP filter metacharacters must not be
	// able to widen the search -- ldap.EscapeFilter neutralizes it, so the
	// literal (unescaped) value never matches anything and the login is
	// rejected as not-found rather than, say, matching every user.
	srv := &testutil.FakeLDAPServer{
		BindDN: "cn=admin,dc=example,dc=org", BindPassword: "adminpass",
		Users: []testutil.FakeLDAPUser{
			{DN: "uid=jdoe,ou=people,dc=example,dc=org", Mail: "jdoe@example.org", Password: "s3cret"},
		},
	}
	host, port := testutil.StartFakeLDAPServer(t, srv)

	_, err := authn.AuthenticateLDAP(testParams(host, port), "*)(mail=*", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not found or ambiguous match")
}
