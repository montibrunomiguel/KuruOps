package dbmigrate

import (
	"fmt"
	"strings"
)

// ParseRoleScript turns one of db/init/*.sql into the statements to execute, in order.
//
// Those scripts are written for psql: `\set` meta-commands, and `:'name'` client-side
// interpolation for the role passwords. A Go program has neither, so this does what psql
// would: drops the meta-commands and comments, replaces each `:'name'` with the matching
// value from vars as a quoted SQL literal, and splits on the `;` that ends a line.
//
// It exists so that the roles and grants have ONE definition. SetupRoles used to carry a
// hand-ported copy of the scripts, and the copy fell behind: the worker role it created
// lacked the DELETE grants the retention sweep needs, the UPDATE on ai_analysis_runs the
// stale-run sweep needs, and the ownership of mv_incident_daily_stats -- so a deployment
// migrated to an external database ran with a worker that failed on those jobs. Running the
// real scripts cannot fall behind them.
//
// A variable referenced but absent from vars is an error rather than an empty string: a
// missing password silently becoming ” would create a role anyone can log in as.
func ParseRoleScript(script string, vars map[string]string) ([]string, error) {
	var stmts []string
	var cur []string
	for _, line := range strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `\`) {
			continue // a psql meta-command (\set ON_ERROR_STOP ...)
		}
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		cur = append(cur, line)
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			stmts = append(stmts, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	if len(cur) > 0 {
		return nil, fmt.Errorf("role script ends in an unterminated statement: %q", strings.Join(cur, " "))
	}

	for i, stmt := range stmts {
		out, err := interpolate(stmt, vars)
		if err != nil {
			return nil, err
		}
		stmts[i] = out
	}
	return stmts, nil
}

// interpolate replaces psql's :'name' with vars[name] as a single-quoted SQL literal.
func interpolate(stmt string, vars map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(stmt); {
		if stmt[i] == ':' && i+2 < len(stmt) && stmt[i+1] == '\'' {
			end := strings.IndexByte(stmt[i+2:], '\'')
			if end < 0 {
				return "", fmt.Errorf("unterminated :'name' reference in %q", stmt)
			}
			name := stmt[i+2 : i+2+end]
			val, ok := vars[name]
			if !ok {
				return "", fmt.Errorf("role script references :'%s' but no value was given", name)
			}
			b.WriteString("'" + strings.ReplaceAll(val, "'", "''") + "'")
			i += 2 + end + 1
			continue
		}
		b.WriteByte(stmt[i])
		i++
	}
	return b.String(), nil
}
