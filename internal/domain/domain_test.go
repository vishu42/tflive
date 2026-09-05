package domain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func TestOperationTypeValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation OperationType
		want      bool
	}{
		{name: "plan is valid", operation: OperationPlan, want: true},
		{name: "apply is valid", operation: OperationApply, want: true},
		{name: "destroy is valid", operation: OperationDestroy, want: true},
		{name: "empty is invalid", operation: OperationType(""), want: false},
		{name: "unknown is invalid", operation: OperationType("refresh"), want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.operation.Valid(); got != test.want {
				t.Fatalf("Valid() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIDValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   ID
		want bool
	}{
		{name: "non-empty id is valid", id: ID("tenant_123"), want: true},
		{name: "empty id is invalid", id: ID(""), want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.id.Valid(); got != test.want {
				t.Fatalf("Valid() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTemplateRevisionStatusValid(t *testing.T) {
	t.Parallel()

	validStatuses := []TemplateRevisionStatus{
		TemplateRevisionPendingValidation,
		TemplateRevisionValidating,
		TemplateRevisionActive,
		TemplateRevisionInvalid,
	}

	for _, status := range validStatuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			if !status.Valid() {
				t.Fatalf("expected %q to be valid", status)
			}
		})
	}

	if TemplateRevisionStatus("deleted").Valid() {
		t.Fatal("expected unknown template revision status to be invalid")
	}
}

func TestTemplateRegistrationStatusValid(t *testing.T) {
	t.Parallel()

	validStatuses := []TemplateRegistrationStatus{
		TemplateRegistrationPending,
		TemplateRegistrationRunning,
		TemplateRegistrationCompleted,
		TemplateRegistrationInvalid,
		TemplateRegistrationFailed,
	}

	for _, status := range validStatuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			if !status.Valid() {
				t.Fatalf("expected %q to be valid", status)
			}
		})
	}

	if TemplateRegistrationStatus("queued").Valid() {
		t.Fatal("expected unknown template registration status to be invalid")
	}
}

func TestTemplateRunStatusValid(t *testing.T) {
	t.Parallel()

	// Read back from the const block rather than counted, because a count is
	// derived from the slice it would be checking: adding a constant and
	// forgetting the slice leaves the length untouched and every test green.
	// The slice is what the migrations' status check constraint and
	// template_runs_in_flight_idx's terminal predicate are written against, so
	// a status missing from it is a status nothing downstream ever sees.
	declared := declaredTemplateRunStatuses(t)
	listed := make(map[TemplateRunStatus]bool, len(AllTemplateRunStatuses))
	for _, status := range AllTemplateRunStatuses {
		listed[status] = true
	}
	for _, status := range declared {
		if !listed[status] {
			t.Errorf("%q is declared as a TemplateRunStatus but missing from AllTemplateRunStatuses - add it there, to the status check constraint in the migrations, and decide whether it is terminal", status)
		}
	}
	if len(AllTemplateRunStatuses) != len(declared) {
		t.Errorf("AllTemplateRunStatuses has %d entries, %d statuses are declared - it has a duplicate or a status no constant declares", len(AllTemplateRunStatuses), len(declared))
	}

	for _, status := range AllTemplateRunStatuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			if !status.Valid() {
				t.Fatalf("expected %q to be valid", status)
			}
		})
	}

	if TemplateRunStatus("started").Valid() {
		t.Fatal("expected unknown template run status to be invalid")
	}
}

// declaredTemplateRunStatuses reads the TemplateRunStatus constants out of the
// source, which is the one place a new status has to appear. Comparing the
// slice against it is what makes the drift detectable at all; comparing the
// slice against itself is not.
func declaredTemplateRunStatuses(t *testing.T) []TemplateRunStatus {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "template_run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse template_run.go: %v", err)
	}

	var statuses []TemplateRunStatus
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if typeName, ok := value.Type.(*ast.Ident); !ok || typeName.Name != "TemplateRunStatus" {
				continue
			}
			for _, expression := range value.Values {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("TemplateRunStatus constant is not a string literal: %#v", expression)
				}
				unquoted, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", literal.Value, err)
				}
				statuses = append(statuses, TemplateRunStatus(unquoted))
			}
		}
	}
	if len(statuses) == 0 {
		t.Fatal("found no TemplateRunStatus constants - the parser is looking in the wrong place")
	}
	return statuses
}

func TestTemplateRunStatusTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status TemplateRunStatus
		want   bool
	}{
		{name: "completed is terminal", status: TemplateRunCompleted, want: true},
		{name: "failed is terminal", status: TemplateRunFailed, want: true},
		{name: "canceled is terminal", status: TemplateRunCanceled, want: true},
		{name: "queued is not terminal", status: TemplateRunQueued, want: false},
		{name: "approval wait is not terminal", status: TemplateRunWaitingApproval, want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.status.Terminal(); got != test.want {
				t.Fatalf("Terminal() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStackTemplateWorkspaceStable(t *testing.T) {
	t.Parallel()

	stackTemplate := StackTemplate{
		ID:                        StackTemplateID("stack_template_123"),
		StackID:                   StackID("stack_123"),
		DesiredTemplateRevisionID: TemplateRevisionID("template_rev_123"),
		WorkspaceName:             "mtp_acme_prod_vpc_a13f9c",
		Lifecycle:                 StackTemplateActive,
	}

	if stackTemplate.WorkspaceName == "" {
		t.Fatal("workspace name should be stored on the StackTemplate")
	}

	if !stackTemplate.Lifecycle.Valid() {
		t.Fatalf("expected lifecycle %q to be valid", stackTemplate.Lifecycle)
	}
}

func TestWorkflowNames(t *testing.T) {
	t.Parallel()

	if TemplateRunWorkflowName != "TemplateRunWorkflow" {
		t.Fatalf("TemplateRunWorkflowName = %q", TemplateRunWorkflowName)
	}

	if TemplateSyncWorkflowName != "TemplateSyncWorkflow" {
		t.Fatalf("TemplateSyncWorkflowName = %q", TemplateSyncWorkflowName)
	}
}

func TestTemplateSyncActivityNames(t *testing.T) {
	t.Parallel()

	if SyncTemplateActivityName != "SyncTemplate" {
		t.Fatalf("SyncTemplateActivityName = %q", SyncTemplateActivityName)
	}

	if RecordTemplateRegistrationStatusActivityName != "RecordTemplateRegistrationStatus" {
		t.Fatalf("RecordTemplateRegistrationStatusActivityName = %q", RecordTemplateRegistrationStatusActivityName)
	}
}

func TestTemplateRunActivityNames(t *testing.T) {
	t.Parallel()

	if FetchSourceActivityName != "FetchSource" {
		t.Fatalf("FetchSourceActivityName = %q", FetchSourceActivityName)
	}
}

func TestTemplateRunWorkflowInputUsesTraitTypes(t *testing.T) {
	t.Parallel()

	input := TemplateRunWorkflowInput{
		RunID:           TemplateRunID("run_123"),
		TenantID:        TenantID("tenant_123"),
		StackTemplateID: StackTemplateID("stack_template_123"),
		Operation:       OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
		RepoOwner:       "acme",
		RepoName:        "infra",
		RootPath:        "modules/vpc",
	}

	if input.Operation != OperationApply {
		t.Fatalf("Operation = %q, want %q", input.Operation, OperationApply)
	}

	if input.WorkspaceName == "" {
		t.Fatal("expected workspace name to be carried into workflow input")
	}

	if input.RootPath != "modules/vpc" {
		t.Fatalf("RootPath = %q, want modules/vpc", input.RootPath)
	}
}

func TestTemplateSyncWorkflowInputUsesRegistrationSource(t *testing.T) {
	t.Parallel()

	input := TemplateSyncWorkflowInput{
		RegistrationID: templateRegistrationID("template_registration_123"),
		TenantID:       TenantID("tenant_123"),
		RepoOwner:      "acme",
		RepoName:       "infra",
		SourceRef:      "v0.0.1",
		RootPath:       "modules/vpc",
	}

	if input.RegistrationID == "" {
		t.Fatal("expected registration id to be carried into workflow input")
	}
	if input.SourceRef != "v0.0.1" {
		t.Fatalf("SourceRef = %q, want v0.0.1", input.SourceRef)
	}
}

func templateRegistrationID(id string) TemplateRegistrationID {
	return TemplateRegistrationID(id)
}

func TestSignalNames(t *testing.T) {
	t.Parallel()

	if ApprovalSignalName != "approval" {
		t.Fatalf("ApprovalSignalName = %q", ApprovalSignalName)
	}

	if CancelSignalName != "cancel" {
		t.Fatalf("CancelSignalName = %q", CancelSignalName)
	}
}
