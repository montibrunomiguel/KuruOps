package jsonpath_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/jsonpath"
)

// TestResolve_ArrayIndex covers array addressing, which the resolver used to
// refuse outright. A Field Mapping Template rule naming such a path resolved
// to nothing and was silently skipped, indistinguishable from a field the
// payload never carried -- and array addressing is pervasive in security
// telemetry (CrowdStrike behaviors[], CloudTrail Records[], Wazuh groups[]).
func TestResolve_ArrayIndex(t *testing.T) {
	var root any
	require.NoError(t, json.Unmarshal([]byte(`{
		"detect": {
			"device": {"hostname": "ws-014"},
			"behaviors": [
				{"tactic": "Credential Access", "filename": "rundll32.exe"},
				{"tactic": "Defense Evasion", "filename": "reg.exe"}
			]
		},
		"rule": {"groups": ["malware", "email"]},
		"empty": [],
		"literalDigits": {"0": "a key that is literally zero"}
	}`), &root))

	get := func(t *testing.T, path string) (any, bool) {
		t.Helper()
		return jsonpath.Resolve(root, path)
	}

	t.Run("indexes into an array of objects", func(t *testing.T) {
		v, ok := get(t, "detect.behaviors.0.tactic")
		require.True(t, ok)
		assert.Equal(t, "Credential Access", v)

		v, ok = get(t, "detect.behaviors.1.filename")
		require.True(t, ok)
		assert.Equal(t, "reg.exe", v)
	})

	t.Run("indexes into an array of scalars", func(t *testing.T) {
		v, ok := get(t, "rule.groups.1")
		require.True(t, ok)
		assert.Equal(t, "email", v)
	})

	t.Run("plain object paths still work", func(t *testing.T) {
		v, ok := get(t, "detect.device.hostname")
		require.True(t, ok)
		assert.Equal(t, "ws-014", v)
	})

	t.Run("an out-of-range index resolves to nothing rather than panicking", func(t *testing.T) {
		_, ok := get(t, "detect.behaviors.9.tactic")
		assert.False(t, ok)
		_, ok = get(t, "empty.0")
		assert.False(t, ok)
	})

	t.Run("a negative or non-numeric index into an array resolves to nothing", func(t *testing.T) {
		_, ok := get(t, "rule.groups.-1")
		assert.False(t, ok)
		_, ok = get(t, "rule.groups.first")
		assert.False(t, ok)
	})

	t.Run("a digit is still read as an object key when the node is an object", func(t *testing.T) {
		// A payload with a literal "0" field keeps working -- the digit only
		// becomes an index once the current value is actually an array.
		v, ok := get(t, "literalDigits.0")
		require.True(t, ok)
		assert.Equal(t, "a key that is literally zero", v)
	})

	t.Run("descending past a scalar still fails", func(t *testing.T) {
		_, ok := get(t, "detect.device.hostname.0")
		assert.False(t, ok)
	})
}
