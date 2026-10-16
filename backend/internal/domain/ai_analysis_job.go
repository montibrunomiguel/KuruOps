package domain

import (
	"time"

	"github.com/google/uuid"
)

type AIAnalysisJobStatus string

const (
	// AIAnalysisJobPending is waiting for NextAttemptAt.
	AIAnalysisJobPending AIAnalysisJobStatus = "pending"
	// AIAnalysisJobRunning is claimed by a consumer; LockedUntil is its lease.
	AIAnalysisJobRunning AIAnalysisJobStatus = "running"
	// AIAnalysisJobSucceeded: the alert was analysed.
	AIAnalysisJobSucceeded AIAnalysisJobStatus = "succeeded"
	// AIAnalysisJobSkipped: nothing to do and never will be -- auto-analysis was
	// switched off, no provider is configured, or the alert is gone.
	AIAnalysisJobSkipped AIAnalysisJobStatus = "skipped"
	// AIAnalysisJobFailed: gave up. LastError says why.
	AIAnalysisJobFailed AIAnalysisJobStatus = "failed"
)

// AIAnalysisJob mirrors `ai_analysis_jobs` -- the durable queue behind
// "Analyze every incoming alert automatically". One row per alert, written in
// the same transaction as the alert itself, so an alert that exists always has
// its analysis owed even if the process dies before anything runs. See
// db/migrations/0014_ai_analysis_jobs.up.sql for the lifecycle and
// service.AIAnalysisQueue for the consumer.
type AIAnalysisJob struct {
	ID            int64               `json:"id"`
	TenantID      uuid.UUID           `json:"tenantId"`
	AlertID       uuid.UUID           `json:"alertId"`
	Status        AIAnalysisJobStatus `json:"status"`
	Attempts      int                 `json:"attempts"`
	MaxAttempts   int                 `json:"maxAttempts"`
	NextAttemptAt time.Time           `json:"nextAttemptAt"`
	LockedUntil   *time.Time          `json:"lockedUntil,omitempty"`
	LastError     *string             `json:"lastError,omitempty"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`

	// Reclaimed is not stored: Claim sets it when the job was taken over from a
	// consumer whose lease ran out (it crashed or was killed mid-analysis), as
	// opposed to being picked up fresh. Such a job may have left a run half
	// finished behind it.
	Reclaimed bool `json:"-"`
}

// AIAnalysisJobStats is the queue's health at one moment, for /metrics.
type AIAnalysisJobStats struct {
	Pending   int
	Running   int
	Succeeded int
	Failed    int
	Skipped   int
	// OldestPendingAge is how long the oldest due job has been waiting; zero
	// when nothing is due. A queue that is not draining shows up here first.
	OldestPendingAge time.Duration
}
