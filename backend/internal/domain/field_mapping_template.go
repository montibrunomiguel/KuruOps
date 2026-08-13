package domain

import (
	"time"

	"github.com/google/uuid"
)

// FieldMappingRule pulls one field out of a webhook's raw JSON body and
// surfaces it on the alert's metadata panel under Label -- JSONPath is a
// simple dot-separated path (e.g. "data.user.name", "rule.level"), not a
// full JSONPath expression language; that's enough to reach any scalar
// field in a typical vendor payload without pulling in a JSONPath
// dependency for the one thing this needs (see
// internal/ingest/field_mapping.go's resolveJSONPath).
type FieldMappingRule struct {
	JSONPath string `json:"jsonPath"`
	Label    string `json:"label"`
}

// FieldMappingTemplate mirrors `field_mapping_templates` -- a Settings-
// managed catalog (like Tag) of reusable field-mapping rule sets, assigned
// to zero or more webhook_endpoints (see WebhookEndpoint.FieldMappingTemplateID).
// Applied on ingest in addition to, not instead of, the sender's own
// top-level "metadata" object (see internal/ingest/handler.go's
// extractMetadata) -- a rule's label is skipped if that key already came
// through automatically, so the auto-extracted metadata always wins on
// conflict (see internal/ingest/field_mapping.go's applyFieldMappingTemplate).
type FieldMappingTemplate struct {
	ID        uuid.UUID          `json:"id"`
	TenantID  uuid.UUID          `json:"tenantId"`
	Name      string             `json:"name"`
	Rules     []FieldMappingRule `json:"rules"`
	CreatedBy *uuid.UUID         `json:"createdBy,omitempty"`
	CreatedAt time.Time          `json:"createdAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
}
