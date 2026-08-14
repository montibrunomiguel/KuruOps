package ingest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
)

// Path resolution itself (TestResolveJSONPath) now lives in
// internal/jsonpath/resolve_test.go -- see that package's doc comment for
// why the resolver moved out of this package.

func TestApplyFieldMappingTemplate(t *testing.T) {
	body := []byte(`{
		"metadata": {"environment": "production"},
		"rule": {"level": 12, "groups": ["malware", "email"]}
	}`)
	autoMetadata := json.RawMessage(`{"environment":"production"}`)

	t.Run("adds a rule's field under its label", func(t *testing.T) {
		rules := []domain.FieldMappingRule{{JSONPath: "rule.level", Label: "Rule Level"}}
		out := applyFieldMappingTemplate(body, rules, autoMetadata)
		var merged map[string]any
		require.NoError(t, json.Unmarshal(out, &merged))
		assert.Equal(t, "production", merged["environment"])
		assert.Equal(t, float64(12), merged["Rule Level"])
	})

	t.Run("auto-extracted metadata wins on label conflict", func(t *testing.T) {
		rules := []domain.FieldMappingRule{{JSONPath: "rule.level", Label: "environment"}}
		out := applyFieldMappingTemplate(body, rules, autoMetadata)
		var merged map[string]any
		require.NoError(t, json.Unmarshal(out, &merged))
		// "environment" must stay the auto value ("production"), not rule.level (12)
		assert.Equal(t, "production", merged["environment"])
	})

	t.Run("a rule whose path doesn't exist on this alert is skipped silently", func(t *testing.T) {
		rules := []domain.FieldMappingRule{{JSONPath: "rule.nonexistent", Label: "Missing"}}
		out := applyFieldMappingTemplate(body, rules, autoMetadata)
		var merged map[string]any
		require.NoError(t, json.Unmarshal(out, &merged))
		_, exists := merged["Missing"]
		assert.False(t, exists)
	})

	t.Run("no rules returns metadata unchanged", func(t *testing.T) {
		out := applyFieldMappingTemplate(body, nil, autoMetadata)
		assert.Equal(t, autoMetadata, out)
	})

	t.Run("array-valued field is preserved as JSON", func(t *testing.T) {
		rules := []domain.FieldMappingRule{{JSONPath: "rule.groups", Label: "Categories"}}
		out := applyFieldMappingTemplate(body, rules, autoMetadata)
		var merged map[string]any
		require.NoError(t, json.Unmarshal(out, &merged))
		assert.Equal(t, []any{"malware", "email"}, merged["Categories"])
	})
}
