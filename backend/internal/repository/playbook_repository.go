package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type PlaybookRepository struct{}

func NewPlaybookRepository() *PlaybookRepository {
	return &PlaybookRepository{}
}

func (r *PlaybookRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Playbook, error) {
	row := tx.QueryRow(ctx, `
		select id, tenant_id, title, category, description, keywords, alert_name_pattern, is_default, created_by, created_at, updated_at
		from playbooks where id = $1`, id)

	pb, err := scanPlaybook(row)
	if err != nil || pb == nil {
		return nil, err
	}

	steps, err := r.stepsFor(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	pb.Steps = steps
	return pb, nil
}

func (r *PlaybookRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.Playbook, error) {
	playbooks, err := queryList(ctx, tx, `
		select id, tenant_id, title, category, description, keywords, alert_name_pattern, is_default, created_by, created_at, updated_at
		from playbooks order by title asc`, scanPlaybook)
	if err != nil {
		return nil, err
	}

	// N+1 on step lookup is acceptable here: playbook libraries are small
	// (tens, not thousands) and this list is cached client-side, not
	// re-fetched per alert render.
	for i := range playbooks {
		steps, err := r.stepsFor(ctx, tx, playbooks[i].ID)
		if err != nil {
			return nil, err
		}
		playbooks[i].Steps = steps
	}

	return playbooks, nil
}

// Insert defaults a nil Keywords to an empty slice, not NULL -- playbooks.keywords
// is NOT NULL, and a Go nil slice encodes as SQL NULL even against a column
// with a `default '{}'` (the default only fires when the column is omitted
// from the INSERT entirely, never when explicitly bound to nil -- see
// docs/TROUBLESHOOTING.md). WebhookService.Create already normalizes this
// for the real app path via orEmptySlice, but a caller going straight to
// the repository (as tests do) doesn't get that for free.
func (r *PlaybookRepository) Insert(ctx context.Context, tx pgx.Tx, pb *domain.Playbook) error {
	if pb.Keywords == nil {
		pb.Keywords = []string{}
	}
	if pb.IsDefault {
		if err := r.clearOtherDefaults(ctx, tx, pb.TenantID, uuid.Nil); err != nil {
			return err
		}
	}
	row := tx.QueryRow(ctx, `
		insert into playbooks (tenant_id, title, category, description, keywords, alert_name_pattern, is_default, created_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, created_at, updated_at`,
		pb.TenantID, pb.Title, pb.Category, pb.Description, pb.Keywords, pb.AlertNamePattern, pb.IsDefault, pb.CreatedBy,
	)
	if err := row.Scan(&pb.ID, &pb.CreatedAt, &pb.UpdatedAt); err != nil {
		return fmt.Errorf("insert playbook: %w", err)
	}
	return r.replaceSteps(ctx, tx, pb.ID, pb.TenantID, pb.Steps)
}

func (r *PlaybookRepository) Update(ctx context.Context, tx pgx.Tx, pb *domain.Playbook) error {
	if pb.IsDefault {
		if err := r.clearOtherDefaults(ctx, tx, pb.TenantID, pb.ID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		update playbooks
		set title = $2, category = $3, description = $4, keywords = $5, alert_name_pattern = $6, is_default = $7, updated_at = now()
		where id = $1`,
		pb.ID, pb.Title, pb.Category, pb.Description, pb.Keywords, pb.AlertNamePattern, pb.IsDefault,
	)
	if err != nil {
		return fmt.Errorf("update playbook: %w", err)
	}
	return r.replaceSteps(ctx, tx, pb.ID, pb.TenantID, pb.Steps)
}

// clearOtherDefaults unsets is_default on every other playbook in the
// tenant before this one is saved as the new default -- enforces "at most
// one default per tenant" ahead of the partial unique index
// (playbooks_one_default_per_tenant), rather than relying on that index to
// reject the save with a hard-to-explain constraint-violation error.
// excludeID is uuid.Nil for an as-yet-unassigned new playbook (Insert).
func (r *PlaybookRepository) clearOtherDefaults(ctx context.Context, tx pgx.Tx, tenantID, excludeID uuid.UUID) error {
	_, err := tx.Exec(ctx, `update playbooks set is_default = false where tenant_id = $1 and id <> $2 and is_default`, tenantID, excludeID)
	if err != nil {
		return fmt.Errorf("clear other default playbooks: %w", err)
	}
	return nil
}

func (r *PlaybookRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from playbooks where id = $1`, id)
	return err
}

// MatchForAlertTitle finds the playbook to auto-assign a newly-ingested
// alert to: the most specific alert_name_pattern that ILIKE-matches title
// (longer pattern wins over a shorter/more generic one), falling back to
// whichever playbook is_default when no pattern matches. Returns nil, nil
// when the tenant has neither a matching pattern nor a default playbook --
// Steps is never populated here (callers only need id/title for the
// denormalized Alert.PlaybookID/PlaybookTitle, not the full step list).
func (r *PlaybookRepository) MatchForAlertTitle(ctx context.Context, tx pgx.Tx, title string) (*domain.Playbook, error) {
	row := tx.QueryRow(ctx, `
		select id, tenant_id, title, category, description, keywords, alert_name_pattern, is_default, created_by, created_at, updated_at
		from playbooks
		where (alert_name_pattern <> '' and $1 ilike alert_name_pattern) or is_default
		order by (alert_name_pattern <> '' and $1 ilike alert_name_pattern) desc, length(alert_name_pattern) desc
		limit 1`, title)
	return scanPlaybook(row)
}

// GetStepWebhookConfig loads a single step's webhook_url/webhook_payload_template
// by step id, for PlaybookService.TriggerStepWebhook — found is false when
// the step doesn't exist (wrong id, or a save since replaced it) or has no
// webhook configured (empty webhook_url).
func (r *PlaybookRepository) GetStepWebhookConfig(ctx context.Context, tx pgx.Tx, stepID uuid.UUID) (playbookID uuid.UUID, url, payloadTemplate string, found bool, err error) {
	err = tx.QueryRow(ctx, `
		select playbook_id, webhook_url, webhook_payload_template from playbook_phase_steps where id = $1`,
		stepID,
	).Scan(&playbookID, &url, &payloadTemplate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", "", false, nil
		}
		return uuid.Nil, "", "", false, fmt.Errorf("get step webhook config: %w", err)
	}
	if url == "" {
		return uuid.Nil, "", "", false, nil
	}
	return playbookID, url, payloadTemplate, true, nil
}

// replaceSteps deletes and reinserts every step for the playbook — simpler
// and just as correct as a diff, given the whole phase-by-phase editor is
// resubmitted on every save in the create/edit form this maps to. Step ids
// are always DB-generated fresh here (the incoming steps' own IDs, if any,
// are ignored) -- the frontend always re-reads the playbook after a save,
// so a trigger button always targets a current id.
// flatPlaybookStep pairs a step with the (phase, phase-local order) that
// its position in domain's map-of-slices shape implies -- replaceChildRows
// takes a single flat []T, so replaceSteps flattens the map into this
// before calling it, restarting the position counter per phase the same
// way the old nested loop did.
type flatPlaybookStep struct {
	phase domain.IncidentPhase
	order int
	step  domain.PlaybookStep
}

func (r *PlaybookRepository) replaceSteps(ctx context.Context, tx pgx.Tx, playbookID, tenantID uuid.UUID, steps map[domain.IncidentPhase][]domain.PlaybookStep) error {
	flat := make([]flatPlaybookStep, 0, len(steps))
	for phase, phaseSteps := range steps {
		for i, step := range phaseSteps {
			flat = append(flat, flatPlaybookStep{phase: phase, order: i, step: step})
		}
	}
	return replaceChildRows(ctx, tx, "playbook step",
		`delete from playbook_phase_steps where playbook_id = $1`, playbookID,
		flat, func(f flatPlaybookStep, _ int) error {
			_, err := tx.Exec(ctx, `
				insert into playbook_phase_steps (playbook_id, tenant_id, phase, step_order, action_text, webhook_url, webhook_payload_template)
				values ($1,$2,$3,$4,$5,$6,$7)`,
				playbookID, tenantID, f.phase, f.order, f.step.Text, f.step.WebhookURL, f.step.WebhookPayloadTemplate,
			)
			return err
		})
}

func (r *PlaybookRepository) stepsFor(ctx context.Context, tx pgx.Tx, playbookID uuid.UUID) (map[domain.IncidentPhase][]domain.PlaybookStep, error) {
	rows, err := tx.Query(ctx, `
		select id, phase, action_text, webhook_url, webhook_payload_template from playbook_phase_steps
		where playbook_id = $1 order by phase, step_order asc`,
		playbookID,
	)
	if err != nil {
		return nil, fmt.Errorf("query playbook steps: %w", err)
	}
	defer rows.Close()

	steps := map[domain.IncidentPhase][]domain.PlaybookStep{}
	for rows.Next() {
		var phase domain.IncidentPhase
		var step domain.PlaybookStep
		if err := rows.Scan(&step.ID, &phase, &step.Text, &step.WebhookURL, &step.WebhookPayloadTemplate); err != nil {
			return nil, fmt.Errorf("scan playbook step: %w", err)
		}
		steps[phase] = append(steps[phase], step)
	}
	return steps, rows.Err()
}

func scanPlaybook(row pgx.Row) (*domain.Playbook, error) {
	var pb domain.Playbook
	err := row.Scan(&pb.ID, &pb.TenantID, &pb.Title, &pb.Category, &pb.Description, &pb.Keywords, &pb.AlertNamePattern, &pb.IsDefault, &pb.CreatedBy, &pb.CreatedAt, &pb.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan playbook: %w", err)
	}
	return &pb, nil
}
