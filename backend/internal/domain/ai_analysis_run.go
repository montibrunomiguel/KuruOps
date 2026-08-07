package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AIAnalysisRunStatus string

const (
	AIAnalysisRunRunning   AIAnalysisRunStatus = "running"
	AIAnalysisRunPaused    AIAnalysisRunStatus = "paused"
	AIAnalysisRunCompleted AIAnalysisRunStatus = "completed"
	AIAnalysisRunFailed    AIAnalysisRunStatus = "failed"
)

// AIAnalysisRun mirrors `ai_analysis_runs` — one agentic "Analyze with AI"
// conversation, possibly spanning multiple HTTP requests when it pauses on
// a side-effecting tool call awaiting analyst approval (PendingToolCallID).
// Messages/Tools/ToolRoutes are opaque JSON here (service.AIAnalysisService
// owns their real shape, []llmclient.Message / []llmclient.Tool /
// map[string]uuid.UUID) -- domain doesn't depend on llmclient, same reason
// AIToolCall.Args/Result stay json.RawMessage.
type AIAnalysisRun struct {
	ID                int64               `json:"id"`
	TenantID          uuid.UUID           `json:"tenantId"`
	ContextType       string              `json:"contextType"` // "alert" | "incident"
	ContextID         uuid.UUID           `json:"contextId"`
	ActorID           uuid.UUID           `json:"actorId"`
	Status            AIAnalysisRunStatus `json:"status"`
	Messages          json.RawMessage     `json:"-"`
	Tools             json.RawMessage     `json:"-"`
	ToolRoutes        json.RawMessage     `json:"-"`
	PendingToolCallID *int64              `json:"pendingToolCallId,omitempty"`
	Result            *string             `json:"result,omitempty"`
	Error             *string             `json:"error,omitempty"`
	CreatedAt         time.Time           `json:"createdAt"`
	UpdatedAt         time.Time           `json:"updatedAt"`
}
