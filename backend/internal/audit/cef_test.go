package audit_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/audit"
	"github.com/kuruops/kuruops/internal/domain"
)

func TestFormatCEF(t *testing.T) {
	actorID := uuid.New()
	contextID := uuid.New()
	createdAt := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)

	e := domain.AuditEvent{
		EventID: "42", Kind: "alert", ContextID: contextID, ContextTitle: "Suspicious login",
		EventType: "status_changed", ActorType: domain.ActorUser, ActorID: &actorID,
		Data: json.RawMessage(`{"from":"open","to":"investigating"}`), CreatedAt: createdAt,
	}

	line := audit.FormatCEF(e)

	require.True(t, strings.HasPrefix(line, "CEF:0|KuruOps|KuruOps|1.0|alert.status_changed|"), "got: %s", line)
	assert.Contains(t, line, "Suspicious login: status changed")
	assert.Contains(t, line, "rt="+strconv.FormatInt(createdAt.UnixMilli(), 10))
	assert.Contains(t, line, "cat=alert")
	assert.Contains(t, line, "suser="+actorID.String())
	assert.Contains(t, line, "outcome=user")
	assert.Contains(t, line, "cs1="+contextID.String())
	assert.Contains(t, line, "cs2=Suspicious login")
}

func TestFormatCEF_SystemActor(t *testing.T) {
	e := domain.AuditEvent{
		EventID: "1", Kind: "alert", ContextID: uuid.New(), ContextTitle: "t",
		EventType: "received", ActorType: domain.ActorSystem, ActorID: nil,
		Data: json.RawMessage(`{}`), CreatedAt: time.Now(),
	}
	line := audit.FormatCEF(e)
	assert.Contains(t, line, "suser=system")
}

func TestFormatCEF_EscapesPipeAndBackslashInTitle(t *testing.T) {
	e := domain.AuditEvent{
		EventID: "1", Kind: "alert", ContextID: uuid.New(), ContextTitle: `Weird | Title \ With Backslash`,
		EventType: "status_changed", ActorType: domain.ActorSystem,
		Data: json.RawMessage(`{}`), CreatedAt: time.Now(),
	}
	line := audit.FormatCEF(e)

	// The Name field (header, pipe-delimited) must have | and \ escaped.
	header := strings.SplitN(line, "cs1Label", 2)[0]
	assert.Contains(t, header, `Weird \| Title \\ With Backslash`)
}

func TestFormatCEF_EscapesEqualsInExtensionData(t *testing.T) {
	e := domain.AuditEvent{
		EventID: "1", Kind: "alert", ContextID: uuid.New(), ContextTitle: "t",
		EventType: "tags_changed", ActorType: domain.ActorSystem,
		Data: json.RawMessage(`{"key=weird":"value"}`), CreatedAt: time.Now(),
	}
	line := audit.FormatCEF(e)
	assert.Contains(t, line, `key\=weird`)
}
