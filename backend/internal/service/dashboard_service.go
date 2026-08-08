package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

type DashboardService struct {
	pool      *db.Pool
	repo      *repository.DashboardRepository
	alerts    *AlertService
	incidents *IncidentService
}

func NewDashboardService(pool *db.Pool, repo *repository.DashboardRepository, alerts *AlertService, incidents *IncidentService) *DashboardService {
	return &DashboardService{pool: pool, repo: repo, alerts: alerts, incidents: incidents}
}

func (s *DashboardService) Stats(ctx context.Context, tenantID uuid.UUID, filter repository.StatsFilter) (*domain.DashboardStats, error) {
	var stats *domain.DashboardStats
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Stats(ctx, tx, tenantID, filter)
		stats = v
		return err
	})
	return stats, err
}

// Activity returns the Recent Activity feed, most recent first, capped at
// limit (defaults to 20 for a non-positive/zero value, matching the
// Dashboard's default page size). kind is "alert", "incident", or "" (both).
// allowedTags scopes the feed to the caller's tag-based access (empty =
// unrestricted); since/until are the Dashboard's time-range filter (either
// nil = unrestricted on that end) -- see DashboardRepository.RecentActivity.
func (s *DashboardService) Activity(ctx context.Context, tenantID uuid.UUID, limit int, kind string, allowedTags []string, since, until *time.Time) ([]domain.ActivityEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	var events []domain.ActivityEvent
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.RecentActivity(ctx, tx, limit, kind, allowedTags, since, until)
		events = v
		return err
	})
	return events, err
}

// Followup assembles the Dashboard's Follow-up view: SLA-breached incidents
// (any phase short of post_incident) plus alerts still needing a decision
// (open/untriaged, escalated, or investigating -- open is included because
// a freshly-received, un-triaged alert is exactly the kind of thing that
// most needs someone to follow up on it). Reuses AlertService/IncidentService.List
// rather than querying directly, so this stays scoped by allowedTags the
// same way the full Alerts/Incidents sections are -- someone with the
// "followup" capability but not "incidents" still only sees follow-up items
// their tag scope allows, nothing broader. since/until are the Dashboard's
// time-range filter (either nil = unrestricted on that end) -- narrows to
// incidents opened / alerts received within [since, until], same as the
// Alerts/Incidents tabs.
func (s *DashboardService) Followup(ctx context.Context, tenantID uuid.UUID, allowedTags []string, since, until *time.Time) (*domain.FollowupView, error) {
	slaBreached := true
	incidents, err := s.incidents.List(ctx, tenantID, repository.ListIncidentsFilter{
		SLABreached: &slaBreached,
		OpenedSince: since,
		OpenedUntil: until,
		AllowedTags: allowedTags,
		Limit:       200,
	})
	if err != nil {
		return nil, err
	}

	alerts, err := s.alerts.List(ctx, tenantID, repository.ListAlertsFilter{
		Statuses:      []domain.AlertStatus{domain.AlertStatusOpen, domain.AlertStatusEscalated, domain.AlertStatusInvestigating},
		ReceivedSince: since,
		ReceivedUntil: until,
		AllowedTags:   allowedTags,
		Limit:         200,
	})
	if err != nil {
		return nil, err
	}

	return &domain.FollowupView{Alerts: alerts, Incidents: incidents}, nil
}
