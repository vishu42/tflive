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
//   - No changes: the run's snapshot is what is live already, so it becomes
//     the stack template's last applied state, and the run completes. That
//     holds for a plan run too: nothing was applied, but nothing needed to
//     be. (A destroy with nothing left to destroy completes the same way; the
//     workflow records destroy_finished for it, which is what moves the
//     template's lifecycle.)
//   - Changes on a plan run: the counts are stored and the run completes. A
//     plan run saved no plan, so there is nothing to approve.
//   - Changes on an apply or destroy run: the counts are stored and the run
//     waits for approval as the template's pending plan.
func (store *Store) FinishTemplatePlan(ctx context.Context, input domain.FinishPlanActivityInput) (domain.PlanOutcome, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin finish plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status domain.TemplateRunStatus
	err = tx.QueryRow(ctx, `
		select status
		from template_runs
		where tenant_id = $1 and id = $2 and stack_template_id = $3 and operation = $4
		for update
	`, input.TenantID, input.RunID, input.StackTemplateID, input.Operation).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read finished plan run: %w", err)
	}

	var outcome domain.PlanOutcome
	switch {
	case !input.HasChanges:
		if input.Operation != domain.OperationDestroy {
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
	case input.Operation == domain.OperationPlan:
		if _, err := tx.Exec(ctx, `
			update template_runs
			set plan_add = $1, plan_change = $2, plan_destroy = $3
			where tenant_id = $4 and id = $5
		`, input.Summary.Add, input.Summary.Change, input.Summary.Destroy, input.TenantID, input.RunID); err != nil {
			return "", fmt.Errorf("record plan with changes: %w", err)
		}
		outcome = domain.PlanOutcomePlanned
	default:
		outcome = domain.PlanOutcomeWaiting
		if _, err := tx.Exec(ctx, `
			update template_runs
			set status = $1, plan_add = $2, plan_change = $3, plan_destroy = $4
			where tenant_id = $5 and id = $6
		`, domain.TemplateRunWaitingApproval, input.Summary.Add, input.Summary.Change, input.Summary.Destroy, input.TenantID, input.RunID); err != nil {
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
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit finish plan: %w", err)
	}
	return outcome, nil
}

// BeginTemplateApply claims a run for its apply phase by moving it to running:
// from approved, or, for an auto-approved apply run that never had a plan to
// approve, from queued. Losing the claim means the plan was discarded first;
// the conditional update is what makes that race safe, because
// discardTemplateRun makes the same kind of update from the other side.
//
// A run already running is this claim retried after its acknowledgement was
// lost. Nothing else moves an approved or auto-approved run to running, and
// the plan workflow of an approved run has already ended, so it reports the
// claim rather than a lost race.
func (store *Store) BeginTemplateApply(ctx context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, autoApprove bool) (bool, error) {
	from := domain.TemplateRunApproved
	if autoApprove {
		from = domain.TemplateRunQueued
	}
	commandTag, err := store.pool.Exec(ctx, `
		update template_runs
		set status = $1
		where tenant_id = $2 and id = $3 and status in ($4, $1) and auto_approve = $5
	`, domain.TemplateRunRunning, tenantID, runID, from, autoApprove)
	if err != nil {
		return false, fmt.Errorf("claim run for apply: %w", err)
	}
	return commandTag.RowsAffected() == 1, nil
}

// discardTemplateRun discards a run whose plan is waiting for approval, or
// approved but not yet claimed by its apply, ending it canceled. Neither has a
// workflow running: the plan workflow ended when the plan did, and the apply
// workflow has not begun. It reports whether it discarded; false means the run
// has no plan left to discard.
func discardTemplateRun(ctx context.Context, exec pgxExecutor, discard domain.TemplateRunDiscard) (bool, error) {
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
		discard.RequestedBy,
		discard.Reason,
		discard.RequestedAt,
		discard.TenantID,
		discard.RunID,
		domain.TemplateRunWaitingApproval,
		domain.TemplateRunApproved,
	)
	if err != nil {
		return false, fmt.Errorf("discard run: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return false, nil
	}
	if err := releaseRunPlan(ctx, exec, discard.TenantID, discard.RunID); err != nil {
		return false, err
	}
	return true, nil
}
