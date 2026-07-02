package traits

import "testing"

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

func TestTemplateStatusValid(t *testing.T) {
	t.Parallel()

	validStatuses := []TemplateStatus{
		TemplatePendingValidation,
		TemplateValidating,
		TemplateActive,
		TemplateInvalid,
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

	if TemplateStatus("deleted").Valid() {
		t.Fatal("expected unknown template status to be invalid")
	}
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
		ID:            StackTemplateID("stack_template_123"),
		StackID:       StackID("stack_123"),
		TemplateID:    TemplateID("template_123"),
		SelectedRef:   "main",
		WorkspaceName: "mtp_acme_prod_vpc_a13f9c",
		Lifecycle:     StackTemplateActive,
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

func TestTemplateRunWorkflowInputUsesTraitTypes(t *testing.T) {
	t.Parallel()

	input := TemplateRunWorkflowInput{
		RunID:           TemplateRunID("run_123"),
		TenantID:        TenantID("tenant_123"),
		StackTemplateID: StackTemplateID("stack_template_123"),
		Operation:       OperationApply,
		SelectedRef:     "main",
		ResolvedCommit:  "abc123",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if input.Operation != OperationApply {
		t.Fatalf("Operation = %q, want %q", input.Operation, OperationApply)
	}

	if input.WorkspaceName == "" {
		t.Fatal("expected workspace name to be carried into workflow input")
	}
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
