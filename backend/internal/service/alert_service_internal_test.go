package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeGroupKey(t *testing.T) {
	payload := json.RawMessage(`{"host": {"name": "srv-01"}, "rule": {"id": "42"}}`)

	t.Run("empty fields list means dedup is off", func(t *testing.T) {
		assert.Equal(t, "", computeGroupKey(payload, nil))
		assert.Equal(t, "", computeGroupKey(payload, []string{}))
	})

	t.Run("all fields present resolves to a non-empty key", func(t *testing.T) {
		key := computeGroupKey(payload, []string{"host.name", "rule.id"})
		assert.NotEmpty(t, key)
	})

	t.Run("same field values produce the same key", func(t *testing.T) {
		other := json.RawMessage(`{"host": {"name": "srv-01"}, "rule": {"id": "42"}, "extra": "ignored"}`)
		assert.Equal(t, computeGroupKey(payload, []string{"host.name", "rule.id"}), computeGroupKey(other, []string{"host.name", "rule.id"}))
	})

	t.Run("different field values produce different keys", func(t *testing.T) {
		other := json.RawMessage(`{"host": {"name": "srv-02"}, "rule": {"id": "42"}}`)
		assert.NotEqual(t, computeGroupKey(payload, []string{"host.name", "rule.id"}), computeGroupKey(other, []string{"host.name", "rule.id"}))
	})

	t.Run("a missing configured field disables dedup for this payload", func(t *testing.T) {
		assert.Equal(t, "", computeGroupKey(payload, []string{"host.name", "does.not.exist"}))
	})

	t.Run("invalid JSON payload disables dedup", func(t *testing.T) {
		assert.Equal(t, "", computeGroupKey(json.RawMessage(`not json`), []string{"host.name"}))
	})

	t.Run("field order matters -- different order is a different key", func(t *testing.T) {
		assert.NotEqual(t,
			computeGroupKey(payload, []string{"host.name", "rule.id"}),
			computeGroupKey(payload, []string{"rule.id", "host.name"}),
		)
	})
}
