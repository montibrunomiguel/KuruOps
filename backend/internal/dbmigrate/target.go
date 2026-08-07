// Package dbmigrate backs Settings -> External Database: an admin-triggered,
// one-time cutover from the bundled Postgres container to a customer-
// supplied external database. This is deliberately an assisted migration
// with a manual restart, not a hot-swap -- DATABASE_URL is read once at
// process startup (see internal/config) with no reload mechanism anywhere,
// so the running api/ingest/worker processes can't retarget themselves
// mid-flight. See Service.Migrate for the full sequence.
package dbmigrate

import (
	"fmt"
	"net/url"
)

// TargetConfig describes the customer-supplied external database an admin
// wants to migrate to. The credentials supplied here must be privileged
// enough to CREATE ROLE, CREATE EXTENSION, and set default privileges --
// ordinary application-level access is not enough to stand up a fresh
// schema. This is never the role the running services connect as
// afterward; see Service.Migrate's generated app/worker credentials for
// that.
type TargetConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
}

// DSN renders a libpq-style connection string, used both for the admin
// connection this package drives directly and (with a different
// user/password) for the connection strings shown to the admin at the end
// of a successful migration.
func (t TargetConfig) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(t.User, t.Password),
		Host:   fmt.Sprintf("%s:%d", t.Host, t.Port),
		Path:   "/" + t.Database,
	}
	q := url.Values{}
	sslMode := t.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String()
}
