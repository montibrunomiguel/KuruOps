package domain

import (
	"time"

	"github.com/google/uuid"
)

// SlackConfig mirrors `tenant_slack_config` (see
// db/migrations/0005_slack_config.up.sql) -- the single Slack workspace, if
// any, connected via bot-token OAuth (see SlackConfigService). Get(tenantID)
// returning nil is the gate every future Slack-dependent feature checks
// before offering its UI (see the original feature request: Slack
// capabilities must stay hidden until this is configured).
//
// BotTokenSecretRef is deliberately untagged for JSON -- never sent to the
// frontend, same reasoning as StorageConfig's secret-ref fields.
// InstalledByUserName is resolved via a join in the repository, not a
// stored column (same convention as EscalationStep.ScheduleName) -- purely
// for display ("connected by ...").
type SlackConfig struct {
	TenantID          uuid.UUID `json:"tenantId"`
	BotTokenSecretRef string    `json:"-"`

	TeamID              string    `json:"teamId"`
	TeamName            string    `json:"teamName"`
	BotUserID           string    `json:"botUserId"`
	InstalledByUserID   uuid.UUID `json:"installedByUserId"`
	InstalledByUserName string    `json:"installedByUserName"`
	GrantedScopes       string    `json:"grantedScopes"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
