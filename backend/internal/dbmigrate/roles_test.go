package dbmigrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/dbmigrate"
)

func TestParseRoleScript(t *testing.T) {
	t.Run("drops meta-commands and comments, splits on statement ends", func(t *testing.T) {
		script := "-- a comment; with a semicolon\n" +
			"\\set ON_ERROR_STOP off\n" +
			"create role r with login; -- trailing comment\n" +
			"\\set ON_ERROR_STOP on\n" +
			"grant select on all tables\n  in schema public to r;\n"
		stmts, err := dbmigrate.ParseRoleScript(script, nil)
		require.NoError(t, err)
		require.Len(t, stmts, 2)
		assert.Equal(t, "create role r with login;", strings.TrimSpace(stmts[0]))
		assert.Contains(t, stmts[1], "grant select on all tables")
		assert.Contains(t, stmts[1], "in schema public to r;", "a statement spanning lines stays one statement")
	})

	t.Run("interpolates :'name' as a quoted literal", func(t *testing.T) {
		stmts, err := dbmigrate.ParseRoleScript("alter role r with password :'pw';\n", map[string]string{"pw": "p'a;ss"})
		require.NoError(t, err)
		assert.Equal(t, "alter role r with password 'p''a;ss';", stmts[0], "a quote in the value is doubled, never closing the literal")
	})

	t.Run("a variable with no value is an error, not an empty password", func(t *testing.T) {
		_, err := dbmigrate.ParseRoleScript("alter role r with password :'pw';\n", map[string]string{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pw")
	})

	t.Run("a type cast is not mistaken for an interpolation", func(t *testing.T) {
		stmts, err := dbmigrate.ParseRoleScript("select 'x'::text;\n", nil)
		require.NoError(t, err)
		assert.Equal(t, "select 'x'::text;", stmts[0])
	})

	t.Run("an unterminated statement is an error", func(t *testing.T) {
		_, err := dbmigrate.ParseRoleScript("grant select on t to r\n", nil)
		require.Error(t, err)
	})
}

// TestRealRoleScriptsParseAndKeepTheirGrants parses the actual db/init scripts and checks the
// grants whose absence broke an externally-migrated deployment: the hand-ported copy of these
// scripts had lost them. If someone edits a script into something ParseRoleScript cannot
// read, this fails here instead of at a customer's migration.
func TestRealRoleScriptsParseAndKeepTheirGrants(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "init", name))
		require.NoError(t, err)
		return string(b)
	}
	vars := map[string]string{"app_password": "A", "worker_password": "W"}

	app, err := dbmigrate.ParseRoleScript(read("kuruops_app_role.sql"), vars)
	require.NoError(t, err)
	worker, err := dbmigrate.ParseRoleScript(read("kuruops_worker_role.sql"), vars)
	require.NoError(t, err)

	joined := strings.ToLower(strings.Join(app, "\n") + "\n" + strings.Join(worker, "\n"))
	for _, want := range []string{
		"create role kuruops_app",
		"alter role kuruops_app with password 'a'",
		"create role kuruops_worker",
		"alter role kuruops_worker with password 'w'",
		"grant delete on alerts, incidents, ai_analysis_runs",                            // the retention sweep
		"grant update (status, error, updated_at) on ai_analysis_runs to kuruops_worker", // the stale-run sweep
		"alter materialized view mv_incident_daily_stats owner to kuruops_worker",
	} {
		assert.Contains(t, joined, want)
	}
	assert.NotContains(t, joined, ":'", "every interpolation was replaced")
}
