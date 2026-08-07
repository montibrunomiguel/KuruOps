package authn

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAPParams is authn's view of an LDAP directory connection — deliberately
// a plain struct (not domain.LDAPConfig) so this package stays free of a
// dependency on internal/domain; internal/service maps one to the other
// after resolving BindPasswordSecretRef via secrets.Store.
type LDAPParams struct {
	Host           string
	Port           int
	UseTLS         bool
	BindDN         string
	BindPassword   string
	UserBaseDN     string
	UserFilter     string // e.g. "(mail=%s)"
	GroupBaseDN    string
	GroupAttribute string // e.g. "memberOf"
}

const ldapDialTimeout = 10 * time.Second

// AuthenticateLDAP performs the standard two-bind pattern: bind as the
// service account to search for the user's DN by email, then re-bind as
// that DN with the user's own password to actually verify the credential —
// the service account bind alone proves nothing about the user's password.
// Returns the user's DN and the raw values of GroupAttribute on their entry
// (typically full group DNs), for the caller to match against
// AuthGroupMapping.ExternalGroup.
type LDAPResult struct {
	DN     string
	Name   string
	Groups []string
}

func AuthenticateLDAP(p LDAPParams, email, password string) (*LDAPResult, error) {
	conn, err := dialLDAP(p)
	if err != nil {
		return nil, fmt.Errorf("connect to ldap: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(p.BindDN, p.BindPassword); err != nil {
		return nil, fmt.Errorf("service account bind failed: %w", err)
	}

	// ldap.EscapeFilter prevents a crafted email (e.g. containing `)(uid=*`)
	// from altering the search filter's structure -- the LDAP-injection
	// equivalent of a parameterized SQL query.
	filter := strings.Replace(p.UserFilter, "%s", ldap.EscapeFilter(email), 1)
	searchReq := ldap.NewSearchRequest(
		p.UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false,
		filter, []string{p.GroupAttribute, "cn", "displayName"}, nil,
	)
	result, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("search for user: %w", err)
	}
	if len(result.Entries) != 1 {
		return nil, fmt.Errorf("user not found or ambiguous match")
	}
	entry := result.Entries[0]

	// Re-bind as the found DN with the caller-supplied password -- this is
	// the actual credential check. A prior connection bound as the service
	// account must not be reused to draw conclusions about this bind.
	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	name := entry.GetAttributeValue("displayName")
	if name == "" {
		name = entry.GetAttributeValue("cn")
	}
	if name == "" {
		name = email
	}

	return &LDAPResult{DN: entry.DN, Name: name, Groups: entry.GetAttributeValues(p.GroupAttribute)}, nil
}

// dialLDAP connects with a bounded timeout, so a misconfigured or
// unreachable directory fails a login request quickly instead of hanging it.
func dialLDAP(p LDAPParams) (*ldap.Conn, error) {
	addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
	opts := []ldap.DialOpt{ldap.DialWithDialer(&net.Dialer{Timeout: ldapDialTimeout})}
	if p.UseTLS {
		opts = append(opts, ldap.DialWithTLSConfig(&tls.Config{ServerName: p.Host, MinVersion: tls.VersionTLS12}))
		return ldap.DialURL(fmt.Sprintf("ldaps://%s", addr), opts...)
	}
	return ldap.DialURL(fmt.Sprintf("ldap://%s", addr), opts...)
}
