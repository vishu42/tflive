package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

func TestStartTemplateRunCreatesRunAndDispatchesWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 7, 2, 9, 30, 0, 0, time.UTC)

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:            traits.StackTemplateID("stack_template_123"),
			StackID:       traits.StackID("stack_123"),
			TemplateID:    traits.TemplateID("template_123"),
			SelectedRef:   "main",
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateActive,
		},
	}
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		Workflows:      workflows,
		RunIDs:         fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:          fixedClock{now: now},
	})

	run, err := service.StartTemplateRun(ctx, StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		TriggerActor:    traits.UserID("user_123"),
	})
	if err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if run.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("run.ID = %q, want run_123", run.ID)
	}

	if run.Status != traits.TemplateRunQueued {
		t.Fatalf("run.Status = %q, want %q", run.Status, traits.TemplateRunQueued)
	}

	if run.WorkspaceName != "mtp_acme_prod_vpc_a13f9c" {
		t.Fatalf("run.WorkspaceName = %q", run.WorkspaceName)
	}

	if run.SelectedRef != "main" {
		t.Fatalf("run.SelectedRef = %q, want main", run.SelectedRef)
	}

	if run.ResolvedCommitSHA != "" {
		t.Fatalf("run.ResolvedCommitSHA = %q, want empty before workflow resolves it", run.ResolvedCommitSHA)
	}

	if !run.StartedAt.Equal(now) {
		t.Fatalf("run.StartedAt = %v, want %v", run.StartedAt, now)
	}

	if stackTemplates.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("stack template lookup tenant = %q", stackTemplates.gotTenantID)
	}

	if runs.created.ID != run.ID {
		t.Fatalf("created run ID = %q, want %q", runs.created.ID, run.ID)
	}

	if workflows.input.RunID != run.ID {
		t.Fatalf("workflow run ID = %q, want %q", workflows.input.RunID, run.ID)
	}

	if workflows.input.Operation != traits.OperationApply {
		t.Fatalf("workflow operation = %q, want %q", workflows.input.Operation, traits.OperationApply)
	}

	if workflows.input.SelectedRef != "main" {
		t.Fatalf("workflow selected ref = %q, want main", workflows.input.SelectedRef)
	}
}

func TestStartTemplateRunRejectsInvalidOperation(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		StackTemplates: &recordingStackTemplateRepository{},
		TemplateRuns:   &recordingTemplateRunRepository{},
		Workflows:      &recordingWorkflowDispatcher{},
		RunIDs:         fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(context.Background(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationType("refresh"),
		TriggerActor:    traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestStartTemplateRunRejectsInactiveStackTemplate(t *testing.T) {
	t.Parallel()

	stackTemplates := &recordingStackTemplateRepository{
		stackTemplate: traits.StackTemplate{
			ID:            traits.StackTemplateID("stack_template_123"),
			SelectedRef:   "main",
			WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
			Lifecycle:     traits.StackTemplateDestroyed,
		},
	}
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		StackTemplates: stackTemplates,
		TemplateRuns:   runs,
		Workflows:      workflows,
		RunIDs:         fixedTemplateRunIDGenerator{runID: traits.TemplateRunID("run_123")},
		Clock:          fixedClock{now: time.Now()},
	})

	_, err := service.StartTemplateRun(context.Background(), StartTemplateRunCommand{
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		TriggerActor:    traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrStackTemplateNotRunnable) {
		t.Fatalf("error = %v, want ErrStackTemplateNotRunnable", err)
	}

	if runs.created.ID != "" {
		t.Fatalf("created run ID = %q, want no persisted run", runs.created.ID)
	}

	if workflows.input.RunID != "" {
		t.Fatalf("workflow run ID = %q, want no workflow dispatch", workflows.input.RunID)
	}
}

func TestApproveRunRecordsApprovalAndSignalsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 7, 2, 10, 15, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRuns: runs,
		Workflows:    workflows,
		Clock:        fixedClock{now: now},
	})

	err := service.ApproveRun(ctx, ApproveRunCommand{
		TenantID:   traits.TenantID("tenant_123"),
		RunID:      traits.TemplateRunID("run_123"),
		ApprovedBy: traits.UserID("user_123"),
	})
	if err != nil {
		t.Fatalf("ApproveRun returned error: %v", err)
	}

	if runs.approval.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("approval run ID = %q, want run_123", runs.approval.RunID)
	}

	if runs.approval.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("approval tenant ID = %q, want tenant_123", runs.approval.TenantID)
	}

	if runs.approval.ApprovedBy != traits.UserID("user_123") {
		t.Fatalf("approval actor = %q, want user_123", runs.approval.ApprovedBy)
	}

	if !runs.approval.ApprovedAt.Equal(now) {
		t.Fatalf("approval time = %v, want %v", runs.approval.ApprovedAt, now)
	}

	if workflows.approvalRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow approval run ID = %q, want run_123", workflows.approvalRunID)
	}

	if workflows.approvalSignal.ApprovedBy != traits.UserID("user_123") {
		t.Fatalf("workflow approval actor = %q, want user_123", workflows.approvalSignal.ApprovedBy)
	}
}

func TestApproveRunDoesNotSignalWhenRunIsNotApprovable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{approvalErr: ErrRunNotApprovable}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRuns: runs,
		Workflows:    workflows,
		Clock:        fixedClock{now: time.Now()},
	})

	err := service.ApproveRun(context.Background(), ApproveRunCommand{
		TenantID:   traits.TenantID("tenant_123"),
		RunID:      traits.TemplateRunID("run_123"),
		ApprovedBy: traits.UserID("user_123"),
	})
	if !errors.Is(err, ErrRunNotApprovable) {
		t.Fatalf("error = %v, want ErrRunNotApprovable", err)
	}

	if workflows.approvalRunID != "" {
		t.Fatalf("workflow approval run ID = %q, want no workflow signal", workflows.approvalRunID)
	}
}

func TestApproveRunRejectsMissingActor(t *testing.T) {
	t.Parallel()

	service := NewService(Service{
		TemplateRuns: &recordingTemplateRunRepository{},
		Workflows:    &recordingWorkflowDispatcher{},
		Clock:        fixedClock{now: time.Now()},
	})

	err := service.ApproveRun(context.Background(), ApproveRunCommand{
		TenantID: traits.TenantID("tenant_123"),
		RunID:    traits.TemplateRunID("run_123"),
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestCancelRunRecordsCancellationAndSignalsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 7, 2, 10, 45, 0, 0, time.UTC)
	runs := &recordingTemplateRunRepository{}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRuns: runs,
		Workflows:    workflows,
		Clock:        fixedClock{now: now},
	})

	err := service.CancelRun(ctx, CancelRunCommand{
		TenantID:    traits.TenantID("tenant_123"),
		RunID:       traits.TemplateRunID("run_123"),
		RequestedBy: traits.UserID("user_456"),
		Reason:      "superseded by a newer run",
	})
	if err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}

	if runs.cancellation.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("cancellation run ID = %q, want run_123", runs.cancellation.RunID)
	}

	if runs.cancellation.RequestedBy != traits.UserID("user_456") {
		t.Fatalf("cancellation actor = %q, want user_456", runs.cancellation.RequestedBy)
	}

	if runs.cancellation.Reason != "superseded by a newer run" {
		t.Fatalf("cancellation reason = %q", runs.cancellation.Reason)
	}

	if !runs.cancellation.RequestedAt.Equal(now) {
		t.Fatalf("cancellation time = %v, want %v", runs.cancellation.RequestedAt, now)
	}

	if workflows.cancelRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow cancel run ID = %q, want run_123", workflows.cancelRunID)
	}

	if workflows.cancelSignal.RequestedBy != traits.UserID("user_456") {
		t.Fatalf("workflow cancel actor = %q, want user_456", workflows.cancelSignal.RequestedBy)
	}
}

func TestCancelRunDoesNotSignalWhenRunIsNotCancelable(t *testing.T) {
	t.Parallel()

	runs := &recordingTemplateRunRepository{cancellationErr: ErrRunNotCancelable}
	workflows := &recordingWorkflowDispatcher{}

	service := NewService(Service{
		TemplateRuns: runs,
		Workflows:    workflows,
		Clock:        fixedClock{now: time.Now()},
	})

	err := service.CancelRun(context.Background(), CancelRunCommand{
		TenantID:    traits.TenantID("tenant_123"),
		RunID:       traits.TemplateRunID("run_123"),
		RequestedBy: traits.UserID("user_456"),
	})
	if !errors.Is(err, ErrRunNotCancelable) {
		t.Fatalf("error = %v, want ErrRunNotCancelable", err)
	}

	if workflows.cancelRunID != "" {
		t.Fatalf("workflow cancel run ID = %q, want no workflow signal", workflows.cancelRunID)
	}
}

type recordingStackTemplateRepository struct {
	stackTemplate traits.StackTemplate
	gotTenantID   traits.TenantID
	gotID         traits.StackTemplateID
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	return repository.stackTemplate, nil
}

type recordingTemplateRunRepository struct {
	created         traits.TemplateRun
	approval        traits.TemplateRunApproval
	cancellation    traits.TemplateRunCancellation
	approvalErr     error
	cancellationErr error
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run traits.TemplateRun) error {
	repository.created = run
	return nil
}

func (repository *recordingTemplateRunRepository) ApproveTemplateRun(_ context.Context, approval traits.TemplateRunApproval) error {
	if repository.approvalErr != nil {
		return repository.approvalErr
	}
	repository.approval = approval
	return nil
}

func (repository *recordingTemplateRunRepository) RequestTemplateRunCancellation(_ context.Context, cancellation traits.TemplateRunCancellation) error {
	if repository.cancellationErr != nil {
		return repository.cancellationErr
	}
	repository.cancellation = cancellation
	return nil
}

type recordingWorkflowDispatcher struct {
	input          traits.TemplateRunWorkflowInput
	approvalRunID  traits.TemplateRunID
	approvalSignal traits.ApprovalSignal
	cancelRunID    traits.TemplateRunID
	cancelSignal   traits.CancelSignal
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	dispatcher.input = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) ApproveTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.ApprovalSignal) error {
	dispatcher.approvalRunID = runID
	dispatcher.approvalSignal = signal
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) CancelTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.CancelSignal) error {
	dispatcher.cancelRunID = runID
	dispatcher.cancelSignal = signal
	return nil
}

type fixedTemplateRunIDGenerator struct {
	runID traits.TemplateRunID
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() traits.TemplateRunID {
	return generator.runID
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}
