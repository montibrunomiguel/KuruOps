package jsonpath_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/jsonpath"
)

func TestResolve(t *testing.T) {
	var root any
	require.NoError(t, json.Unmarshal([]byte(`{
		"rule": {"level": 12, "groups": ["malware", "email"]},
		"agent": {"name": "mail-gw-01"}
	}`), &root))

	t.Run("nested scalar path resolves", func(t *testing.T) {
		v, ok := jsonpath.Resolve(root, "rule.level")
		require.True(t, ok)
		assert.Equal(t, float64(12), v)
	})

	t.Run("top-level object path resolves", func(t *testing.T) {
		v, ok := jsonpath.Resolve(root, "agent.name")
		require.True(t, ok)
		assert.Equal(t, "mail-gw-01", v)
	})

	t.Run("missing path segment fails", func(t *testing.T) {
		_, ok := jsonpath.Resolve(root, "rule.nonexistent")
		assert.False(t, ok)
	})

	t.Run("path through a non-object value fails", func(t *testing.T) {
		_, ok := jsonpath.Resolve(root, "rule.level.nested")
		assert.False(t, ok)
	})

	t.Run("array leaf value resolves as-is", func(t *testing.T) {
		v, ok := jsonpath.Resolve(root, "rule.groups")
		require.True(t, ok)
		assert.Equal(t, []any{"malware", "email"}, v)
	})
}
