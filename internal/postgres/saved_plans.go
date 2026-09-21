package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/planbundle"
)

// CreatePlanKey returns the key the run's saved plan is encrypted with, making
// one on first use. The key is stored encrypted with the control plane's
// credential cipher; only the control plane can read it back.
//
// Making it is idempotent under concurrency: the update only fills an empty
// column, and whichever writer lost reads back the winner's key.
func (store *Store) CreatePlanKey(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]byte, error) {
	if key, err := store.PlanKey(ctx, tenantID, runID); err == nil {
		return key, nil
	} else if !errors.Is(err, errNoPlanKey) {
		return nil, err
	}

	key, err := planbundle.NewKey()
	if err != nil {
		return nil, err
	}
	encrypted, err := store.Encrypt(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		return nil, fmt.Errorf("encrypt plan key: %w", err)
	}
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set plan_artifact_dek = $1
		where tenant_id = $2
			and id = $3
			and plan_artifact_dek is null
			and status not in ('completed', 'failed', 'canceled')
	`, encrypted, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("store plan key: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return store.PlanKey(ctx, tenantID, runID)
	}
	return key, nil
}

// errNoPlanKey is a run without a plan key: none was made yet, or the run is
// terminal and its key was dropped.
var errNoPlanKey = errors.New("run has no plan key")

// PlanKey returns the run's plan key.
func (store *Store) PlanKey(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]byte, error) {
	var encrypted *string
	err := store.pool.QueryRow(ctx, `
		select plan_artifact_dek from template_runs where tenant_id = $1 and id = $2
	`, tenantID, runID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read plan key: %w", err)
	}
	if encrypted == nil {
		return nil, errNoPlanKey
	}
	encoded, err := store.Decrypt(*encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt plan key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode plan key: %w", err)
	}
	return key, nil
}

// FinishTemplatePlan records a finished plan and decides what follows it, in
// one transaction with the run row locked:
//
//   - A run someone canceled while it planned is canceled here, since no
//     workflow step is left to notice the request.
//   - No changes: the run's snapshot is what is live already, so it becomes
//     the stack template's last applied state, and the run completes. (A
//     destroy with nothing left to destroy completes the same way; the
//     workflow records destroy_finished for it, which is what moves the
//     template's lifecycle.)
//   - Changes: the counts are stored and the run waits for approval as the
//     template's pending plan.
//   - Changes on an auto-approved run: approved on the spot, by the person
//     who started it, with the same audit record a manual approval leaves.
//
// Auto-approval needs no check that the plan still matches desired state:
// config and revision cannot change while a run is in flight.
func (store *Store) FinishTemplatePlan(ctx context.Context, input domain.FinishPlanActivityInput) (domain.PlanOutcome, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin finish plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status domain.TemplateRunStatus
	var triggerActor domain.UserID
	err = tx.QueryRow(ctx, `
		select status, trigger_actor
		from template_runs
		where tenant_id = $1 and id = $2 and stack_template_id = $3 and operation = $4
		for update
	`, input.TenantID, input.RunID, input.StackTemplateID, input.Operation).Scan(&status, &triggerActor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read finished plan run: %w", err)
	}

	var outcome domain.PlanOutcome
	switch {
	case status == domain.TemplateRunCancelRequested:
		if _, err := tx.Exec(ctx, `
			update template_runs
			set status = $1, completed_at = coalesce(completed_at, now())
			where tenant_id = $2 and id = $3
		`, domain.TemplateRunCanceled, input.TenantID, input.RunID); err != nil {
			return "", fmt.Errorf("cancel finished plan: %w", err)
		}
		if err := releaseRunPlan(ctx, tx, input.TenantID, input.RunID); err != nil {
			return "", err
		}
		outcome = domain.PlanOutcomeCanceled
	case !input.HasChanges:
		if input.Operation == domain.OperationPlan {
			if _, err := tx.Exec(ctx, `
				update stack_templates
				set
					last_applied_run_id = template_runs.id,
					last_applied_template_revision_id = template_runs.template_revision_id,
					last_applied_config_json = template_runs.config_json,
					last_applied_at = now()
				from template_runs
				where template_runs.tenant_id = $1
					and template_runs.id = $2
					and stack_templates.tenant_id = template_runs.tenant_id
					and stack_templates.id = template_runs.stack_template_id
			`, input.TenantID, input.RunID); err != nil {
				return "", fmt.Errorf("record plan without changes as applied: %w", err)
			}
		}
		outcome = domain.PlanOutcomeNoChanges
	default:
		next := domain.TemplateRunWaitingApproval
		outcome = domain.PlanOutcomeWaiting
		if input.AutoApprove {
			next = domain.TemplateRunApproved
			outcome = domain.PlanOutcomeApproved
		}
		if _, err := tx.Exec(ctx, `
			update template_runs
			set status = $1, plan_add = $2, plan_change = $3, plan_destroy = $4
			where tenant_id = $5 and id = $6
		`, next, input.Summary.Add, input.Summary.Change, input.Summary.Destroy, input.TenantID, input.RunID); err != nil {
			return "", fmt.Errorf("record plan with changes: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			update stack_templates
			set
				pending_plan_run_id = template_runs.id,
				pending_plan_template_revision_id = template_runs.template_revision_id,
				pending_plan_config_json = template_runs.config_json,
				pending_plan_at = now()
			from template_runs
			where template_runs.tenant_id = $1
				and template_runs.id = $2
				and stack_templates.tenant_id = template_runs.tenant_id
				and stack_templates.id = template_runs.stack_template_id
		`, input.TenantID, input.RunID); err != nil {
			return "", fmt.Errorf("record pending plan: %w", err)
		}
		if input.AutoApprove {
			if err := appendAuditEvent(ctx, tx, domain.SecurityAuditEvent{
				ActorSubject: string(triggerActor),
				Action:       domain.AuditActionApprovalGranted,
				TenantID:     input.TenantID,
				Outcome:      domain.AuditOutcomeSuccess,
			}); err != nil {
				return "", fmt.Errorf("audit auto-approval: %w", err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit finish plan: %w", err)
	}
	return outcome, nil
}

// BeginTemplateApply claims an approved run for its apply phase by moving it
// from approved to locked. Losing the claim means the run was canceled between
// approval and now; the conditional update is what makes that race safe,
// because cancelTemplateRunBeforeApply makes the same kind of update from the
// other side.
func (store *Store) BeginTemplateApply(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) (bool, error) {
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set status = $1
		where tenant_id = $2 and id = $3 and status = $4
	`, domain.TemplateRunLocked, tenantID, runID, domain.TemplateRunApproved)
	if err != nil {
		return false, fmt.Errorf("claim run for apply: %w", err)
	}
	return commandTag.RowsAffected() == 1, nil
}

// cancelTemplateRunBeforeApply cancels a run whose plan is waiting for
// approval, or approved but not yet claimed by its apply. Neither has a
// workflow to signal: the plan workflow ended when the plan did, and the apply
// workflow has not begun. It reports whether it canceled; false means the run
// is past that point, and has to be canceled through its workflow.
func cancelTemplateRunBeforeApply(ctx context.Context, exec pgxExecutor, cancellation domain.TemplateRunCancellation) (bool, error) {
	commandTag, err := exec.Exec(ctx, `
		update template_runs
		set
			status = $1,
			cancellation_requested_by = $2,
			cancellation_reason = $3,
			cancellation_requested_at = $4,
			completed_at = coalesce(completed_at, now())
		where tenant_id = $5
			and id = $6
			and status in ($7, $8)
	`,
		domain.TemplateRunCanceled,
		cancellation.RequestedBy,
		cancellation.Reason,
		cancellation.RequestedAt,
		cancellation.TenantID,
		cancellation.RunID,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunApproved,
	)
	if err != nil {
		return false, fmt.Errorf("cancel run before apply: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return false, nil
	}
	if err := releaseRunPlan(ctx, exec, cancellation.TenantID, cancellation.RunID); err != nil {
		return false, err
	}
	return true, nil
}
