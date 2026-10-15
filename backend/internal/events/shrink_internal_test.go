package events

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShrinkPayload(t *testing.T) {
	big := strings.Repeat("x", 6000)

	t.Run("drops only the largest field when that is enough", func(t *testing.T) {
		got, dropped := shrinkPayload(map[string]any{"id": "a1", "action": "analyzed", "message": big}, 1000)
		require.NotNil(t, got)
		assert.Equal(t, []string{"message"}, dropped)
		assert.Equal(t, "a1", got["id"])
		assert.Equal(t, "analyzed", got["action"])
		assert.Equal(t, true, got["truncated"])
	})

	t.Run("drops several large fields, largest first, keeping the small ones", func(t *testing.T) {
		got, dropped := shrinkPayload(map[string]any{
			"id": "a1", "runId": 7, "huge": strings.Repeat("y", 9000), "large": strings.Repeat("z", 5000),
		}, 1000)
		require.NotNil(t, got)
		assert.Equal(t, []string{"huge", "large"}, dropped, "the biggest goes first")
		assert.Equal(t, "a1", got["id"])
		assert.EqualValues(t, 7, got["runId"])
	})

	t.Run("a payload that already fits is not marked truncated by the caller path", func(t *testing.T) {
		// shrinkPayload is only called for an oversized message, but called
		// directly with room to spare it must not drop anything it does not
		// have to.
		got, dropped := shrinkPayload(map[string]any{"id": "a1"}, 1000)
		require.NotNil(t, got)
		assert.Empty(t, dropped)
	})

	t.Run("nothing usable left yields no payload", func(t *testing.T) {
		got, dropped := shrinkPayload(map[string]any{"a": big, "b": big}, 100)
		assert.Nil(t, got)
		assert.Len(t, dropped, 2)
	})

	t.Run("non-object payloads yield no payload", func(t *testing.T) {
		for name, payload := range map[string]any{"string": big, "number": 42, "array": []string{big}, "nil": nil} {
			got, _ := shrinkPayload(payload, 1000)
			assert.Nil(t, got, name)
		}
	})

	t.Run("a value that cannot be marshalled yields no payload", func(t *testing.T) {
		got, _ := shrinkPayload(make(chan int), 1000)
		assert.Nil(t, got)
	})
}

func TestMarshalNotify_NeverExceedsThePostgresLimit(t *testing.T) {
	b := &Broadcaster{logger: discardLogger()}
	tenant := uuid.New()

	cases := map[string]any{
		"ordinary":            map[string]any{"id": "a1"},
		"one huge field":      map[string]any{"id": "a1", "message": strings.Repeat("é\n\"", 5000)},
		"every field huge":    map[string]any{"a": strings.Repeat("a", 9000), "b": strings.Repeat("b", 9000)},
		"huge string payload": strings.Repeat("s", 50000),
		"nil":                 nil,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := b.marshalNotify(tenant, "ai_analysis_turn", payload)
			require.NoError(t, err)
			assert.Less(t, len(data), 8000, "Postgres refuses a NOTIFY payload of 8000 bytes or more")

			var msg notifyMessage
			require.NoError(t, json.Unmarshal(data, &msg), "the reduced message must still be valid for the listener to decode")
			assert.Equal(t, tenant, msg.TenantID)
			assert.Equal(t, "ai_analysis_turn", msg.Event.Type, "the event type always survives")
		})
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
