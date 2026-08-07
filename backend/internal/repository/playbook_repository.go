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
		select id, tenant_id, title, category, description, keywords, created_by, created_at, updated_at
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
	rows, err := tx.Query(ctx, `
		select id, tenant_id, title, category, description, keywords, created_by, created_at, updated_at
		from playbooks order by title asc`)
	if err != nil {
		return nil, fmt.Errorf("query playbooks: %w", err)
	}
	defer rows.Close()

	playbooks := []domain.Playbook{}
	for rows.Next() {
		pb, err := scanPlaybook(rows)
		if err != nil {
			return nil, err
		}
		playbooks = append(playbooks, *pb)
	}
	if err := rows.Err(); err != nil {
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

func (r *PlaybookRepository) Insert(ctx context.Context, tx pgx.Tx, pb *domain.Playbook) error {
	row := tx.QueryRow(ctx, `
		insert into playbooks (tenant_id, title, category, description, keywords, created_by)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at, updated_at`,
		pb.TenantID, pb.Title, pb.Category, pb.Description, pb.Keywords, pb.CreatedBy,
	)
	if err := row.Scan(&pb.ID, &pb.CreatedAt, &pb.UpdatedAt); err != nil {
		return fmt.Errorf("insert playbook: %w", err)
	}
	return r.replaceSteps(ctx, tx, pb.ID, pb.TenantID, pb.Steps)
}

func (r *PlaybookRepository) Update(ctx context.Context, tx pgx.Tx, pb *domain.Playbook) error {
	_, err := tx.Exec(ctx, `
		update playbooks
		set title = $2, category = $3, description = $4, keywords = $5, updated_at = now()
		where id = $1`,
		pb.ID, pb.Title, pb.Category, pb.Description, pb.Keywords,
	)
	if err != nil {
		return fmt.Errorf("update playbook: %w", err)
	}
	return r.replaceSteps(ctx, tx, pb.ID, pb.TenantID, pb.Steps)
}

func (r *PlaybookRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from playbooks where id = $1`, id)
	return err
}

// replaceSteps deletes and reinserts every step for the playbook — simpler
// and just as correct as a diff, given the whole phase-by-phase textarea is
// resubmitted on every save in the create/edit form this maps to.
func (r *PlaybookRepository) replaceSteps(ctx context.Context, tx pgx.Tx, playbookID, tenantID uuid.UUID, steps map[domain.IncidentPhase][]string) error {
	if _, err := tx.Exec(ctx, `delete from playbook_phase_steps where playbook_id = $1`, playbookID); err != nil {
		return fmt.Errorf("clear playbook steps: %w", err)
	}
	for phase, actions := range steps {
		for i, action := range actions {
			if _, err := tx.Exec(ctx, `
				insert into playbook_phase_steps (playbook_id, tenant_id, phase, step_order, action_text)
				values ($1,$2,$3,$4,$5)`,
				playbookID, tenantID, phase, i, action,
			); err != nil {
				return fmt.Errorf("insert playbook step: %w", err)
			}
		}
	}
	return nil
}

func (r *PlaybookRepository) stepsFor(ctx context.Context, tx pgx.Tx, playbookID uuid.UUID) (map[domain.IncidentPhase][]string, error) {
	rows, err := tx.Query(ctx, `
		select phase, action_text from playbook_phase_steps
		where playbook_id = $1 order by phase, step_order asc`,
		playbookID,
	)
	if err != nil {
		return nil, fmt.Errorf("query playbook steps: %w", err)
	}
	defer rows.Close()

	steps := map[domain.IncidentPhase][]string{}
	for rows.Next() {
		var phase domain.IncidentPhase
		var action string
		if err := rows.Scan(&phase, &action); err != nil {
			return nil, fmt.Errorf("scan playbook step: %w", err)
		}
		steps[phase] = append(steps[phase], action)
	}
	return steps, rows.Err()
}

func scanPlaybook(row pgx.Row) (*domain.Playbook, error) {
	var pb domain.Playbook
	err := row.Scan(&pb.ID, &pb.TenantID, &pb.Title, &pb.Category, &pb.Description, &pb.Keywords, &pb.CreatedBy, &pb.CreatedAt, &pb.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan playbook: %w", err)
	}
	return &pb, nil
}
