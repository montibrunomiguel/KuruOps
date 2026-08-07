package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ToolCallStatus string

const (
	ToolCallProposed ToolCallStatus = "proposed"
	ToolCallApproved ToolCallStatus = "approved"
	ToolCallRejected ToolCallStatus = "rejected"
	ToolCallExecuted ToolCallStatus = "executed"
	ToolCallFailed   ToolCallStatus = "failed"
)

// AIToolCall mirrors `ai_tool_calls` — the forensic log of every MCP tool
// the analysis agent invoked or proposed. Non-side-effecting tools go
// straight to 'executed'; side-effecting ones (see
// mcp_servers.side_effecting_tools) sit at 'proposed' until an analyst
// approves or rejects them — see service.MCPToolService and the
// architecture review's "IA sugere vs IA executa" principle.
type AIToolCall struct {
	ID          int64           `json:"id"`
	TenantID    uuid.UUID       `json:"tenantId"`
	MCPServerID uuid.UUID       `json:"mcpServerId"`
	ToolName    string          `json:"toolName"`
	ContextType string          `json:"contextType"` // "alert" | "incident"
	ContextID   uuid.UUID       `json:"contextId"`
	Args        json.RawMessage `json:"args"`
	Result      json.RawMessage `json:"result,omitempty"`
	Status      ToolCallStatus  `json:"status"`
	ApprovedBy  *uuid.UUID      `json:"approvedBy,omitempty"`
	ApprovedAt  *time.Time      `json:"approvedAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}
