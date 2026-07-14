# internal/temporal API Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the API-side Temporal adapter that implements `app.WorkflowDispatcher`.

**Architecture:** `internal/temporal` owns SDK dialing and dispatch to Temporal. The app layer continues to depend only on `app.WorkflowDispatcher`; SDK types stay inside this adapter package. Unit tests use a narrow package-owned fake client so no live Temporal server is needed.

**Tech Stack:** Go 1.24, Temporal Go SDK `go.temporal.io/sdk`, standard `testing`.

---

## File Structure

- Create `internal/temporal/client.go`: `Config`, `ErrInvalidConfig`, default namespace handling, and `Dial`.
- Create `internal/temporal/client_test.go`: unit tests for dial validation and context cancellation.
- Create `internal/temporal/dispatcher.go`: `Dispatcher`, workflow ID construction, workflow start, approval signal, and cancellation signal.
- Create `internal/temporal/dispatcher_test.go`: fake workflow client, dispatch behavior tests, interface assertion, and error wrapping tests.
- Modify `internal/temporal/doc.go`: keep package documentation as-is unless gofmt changes it.
- Modify `go.mod` and `go.sum`: add Temporal Go SDK dependency.

### Task 1: Add Temporal Client Dialing

**Files:**
- Create: `internal/temporal/client_test.go`
- Create: `internal/temporal/client.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing dial tests**

Create `internal/temporal/client_test.go`:

```go
package temporal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDialRejectsMissingAddress(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), Config{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestDialReturnsContextErrorBeforeSDKDial(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Dial(ctx, Config{Address: "localhost:7233"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "dial temporal") {
		t.Fatalf("error = %q, want dial temporal context", err.Error())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/temporal
```

Expected: FAIL because `Dial`, `Config`, and `ErrInvalidConfig` are undefined.

- [ ] **Step 3: Add the Temporal SDK dependency**

Run:

```bash
go get go.temporal.io/sdk@latest
```

Expected: `go.mod` and `go.sum` gain `go.temporal.io/sdk` and its transitive dependencies.

- [ ] **Step 4: Implement client dialing**

Create `internal/temporal/client.go`:

```go
package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/client"
)

const defaultNamespace = "default"

var ErrInvalidConfig = errors.New("temporal: invalid config")

type Config struct {
	Address   string
	Namespace string
}

func Dial(ctx context.Context, cfg Config) (client.Client, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("dial temporal: %w", ctx.Err())
	default:
	}

	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:  address,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("dial temporal: %w", err)
	}

	return temporalClient, nil
}
```

- [ ] **Step 5: Run the focused test**

Run:

```bash
go test ./internal/temporal
```

Expected: PASS.

- [ ] **Step 6: Commit the client dialing slice**

Run:

```bash
git add internal/temporal/client.go internal/temporal/client_test.go go.mod go.sum
git commit -m "feat: add temporal client dialing"
```

Expected: commit succeeds with only the client dialing files and dependency updates staged.

### Task 2: Start Template Run Workflows

**Files:**
- Create: `internal/temporal/dispatcher_test.go`
- Create: `internal/temporal/dispatcher.go`

- [ ] **Step 1: Write the failing start workflow test**

Create `internal/temporal/dispatcher_test.go`:

```go
package temporal

import (
	"context"
	"reflect"
	"testing"

	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

func TestStartTemplateRunExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	input := traits.TemplateRunWorkflowInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if err := dispatcher.StartTemplateRun(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != "terraform-runs" {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeWorkflow != traits.TemplateRunWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

func TestTemplateRunWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

type recordingWorkflowClient struct {
	executeOptions  client.StartWorkflowOptions
	executeWorkflow interface{}
	executeArgs     []interface{}
}

func (workflowClient *recordingWorkflowClient) ExecuteWorkflow(
	_ context.Context,
	options client.StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (client.WorkflowRun, error) {
	workflowClient.executeOptions = options
	workflowClient.executeWorkflow = workflow
	workflowClient.executeArgs = args
	return nil, nil
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/temporal
```

Expected: FAIL because `newDispatcher`, `StartTemplateRun`, and `templateRunWorkflowID` are undefined.

- [ ] **Step 3: Implement the minimal dispatcher start behavior**

Create `internal/temporal/dispatcher.go`:

```go
package temporal

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
}

type Dispatcher struct {
	client    workflowClient
	taskQueue string
}

func NewDispatcher(temporalClient client.Client, taskQueue string) *Dispatcher {
	return newDispatcher(temporalClient, taskQueue)
}

func newDispatcher(temporalClient workflowClient, taskQueue string) *Dispatcher {
	return &Dispatcher{
		client:    temporalClient,
		taskQueue: taskQueue,
	}
}

func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        templateRunWorkflowID(input.TenantID, input.RunID),
		TaskQueue: dispatcher.taskQueue,
	}, traits.TemplateRunWorkflowName, input)
	if err != nil {
		return err
	}

	return nil
}

func templateRunWorkflowID(tenantID traits.TenantID, runID traits.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}
```

- [ ] **Step 4: Run the focused test**

Run:

```bash
go test ./internal/temporal
```

Expected: PASS.

- [ ] **Step 5: Commit the workflow start slice**

Run:

```bash
git add internal/temporal/dispatcher.go internal/temporal/dispatcher_test.go
git commit -m "feat: start temporal template run workflows"
```

Expected: commit succeeds with only dispatcher files staged.

### Task 3: Send Approval And Cancellation Signals

**Files:**
- Modify: `internal/temporal/dispatcher_test.go`
- Modify: `internal/temporal/dispatcher.go`

- [ ] **Step 1: Replace the dispatcher test with workflow and signal coverage**

Replace `internal/temporal/dispatcher_test.go` with:

```go
package temporal

import (
	"context"
	"reflect"
	"testing"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

var _ app.WorkflowDispatcher = (*Dispatcher)(nil)

func TestStartTemplateRunExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	input := traits.TemplateRunWorkflowInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if err := dispatcher.StartTemplateRun(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != "terraform-runs" {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeWorkflow != traits.TemplateRunWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

func TestApproveTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.ApprovalSignal{ApprovedBy: traits.UserID("user_123")}

	err := dispatcher.ApproveTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("ApproveTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != traits.ApprovalSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestCancelTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.CancelSignal{
		RequestedBy: traits.UserID("user_456"),
		Reason:      "superseded by a newer run",
	}

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("CancelTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != traits.CancelSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestTemplateRunWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

type recordingWorkflowClient struct {
	executeOptions  client.StartWorkflowOptions
	executeWorkflow interface{}
	executeArgs     []interface{}
	signalWorkflowID string
	signalRunID      string
	signalName       string
	signalArg        interface{}
}

func (workflowClient *recordingWorkflowClient) ExecuteWorkflow(
	_ context.Context,
	options client.StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (client.WorkflowRun, error) {
	workflowClient.executeOptions = options
	workflowClient.executeWorkflow = workflow
	workflowClient.executeArgs = args
	return nil, nil
}

func (workflowClient *recordingWorkflowClient) SignalWorkflow(
	_ context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) error {
	workflowClient.signalWorkflowID = workflowID
	workflowClient.signalRunID = runID
	workflowClient.signalName = signalName
	workflowClient.signalArg = arg
	return nil
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/temporal
```

Expected: FAIL because `Dispatcher` does not implement `ApproveTemplateRun` and `CancelTemplateRun`, and `workflowClient` does not include `SignalWorkflow`.

- [ ] **Step 3: Implement signal dispatch**

Replace `internal/temporal/dispatcher.go` with:

```go
package temporal

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	SignalWorkflow(context.Context, string, string, string, interface{}) error
}

type Dispatcher struct {
	client    workflowClient
	taskQueue string
}

func NewDispatcher(temporalClient client.Client, taskQueue string) *Dispatcher {
	return newDispatcher(temporalClient, taskQueue)
}

func newDispatcher(temporalClient workflowClient, taskQueue string) *Dispatcher {
	return &Dispatcher{
		client:    temporalClient,
		taskQueue: taskQueue,
	}
}

func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        templateRunWorkflowID(input.TenantID, input.RunID),
		TaskQueue: dispatcher.taskQueue,
	}, traits.TemplateRunWorkflowName, input)
	if err != nil {
		return err
	}

	return nil
}

func (dispatcher *Dispatcher) ApproveTemplateRun(
	ctx context.Context,
	tenantID traits.TenantID,
	runID traits.TemplateRunID,
	signal traits.ApprovalSignal,
) error {
	return dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		traits.ApprovalSignalName,
		signal,
	)
}

func (dispatcher *Dispatcher) CancelTemplateRun(
	ctx context.Context,
	tenantID traits.TenantID,
	runID traits.TemplateRunID,
	signal traits.CancelSignal,
) error {
	return dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		traits.CancelSignalName,
		signal,
	)
}

func templateRunWorkflowID(tenantID traits.TenantID, runID traits.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}
```

- [ ] **Step 4: Run the focused test**

Run:

```bash
go test ./internal/temporal
```

Expected: PASS.

- [ ] **Step 5: Commit the signal dispatch slice**

Run:

```bash
git add internal/temporal/dispatcher.go internal/temporal/dispatcher_test.go
git commit -m "feat: signal temporal template run workflows"
```

Expected: commit succeeds with only dispatcher files staged.

### Task 4: Wrap Dispatcher Errors With Operation Context

**Files:**
- Modify: `internal/temporal/dispatcher_test.go`
- Modify: `internal/temporal/dispatcher.go`

- [ ] **Step 1: Replace the dispatcher test with error wrapping coverage**

Replace `internal/temporal/dispatcher_test.go` with:

```go
package temporal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

var _ app.WorkflowDispatcher = (*Dispatcher)(nil)

func TestStartTemplateRunExecutesWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	input := traits.TemplateRunWorkflowInput{
		RunID:           traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationApply,
		SelectedRef:     "main",
		WorkspaceName:   "mtp_acme_prod_vpc_a13f9c",
	}

	if err := dispatcher.StartTemplateRun(context.Background(), input); err != nil {
		t.Fatalf("StartTemplateRun returned error: %v", err)
	}

	if workflowClient.executeOptions.ID != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", workflowClient.executeOptions.ID)
	}
	if workflowClient.executeOptions.TaskQueue != "terraform-runs" {
		t.Fatalf("task queue = %q", workflowClient.executeOptions.TaskQueue)
	}
	if workflowClient.executeWorkflow != traits.TemplateRunWorkflowName {
		t.Fatalf("workflow name = %#v", workflowClient.executeWorkflow)
	}
	if len(workflowClient.executeArgs) != 1 {
		t.Fatalf("workflow arg count = %d, want 1", len(workflowClient.executeArgs))
	}
	if !reflect.DeepEqual(workflowClient.executeArgs[0], input) {
		t.Fatalf("workflow input = %#v, want %#v", workflowClient.executeArgs[0], input)
	}
}

func TestApproveTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.ApprovalSignal{ApprovedBy: traits.UserID("user_123")}

	err := dispatcher.ApproveTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("ApproveTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != traits.ApprovalSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestCancelTemplateRunSignalsWorkflow(t *testing.T) {
	t.Parallel()

	workflowClient := &recordingWorkflowClient{}
	dispatcher := newDispatcher(workflowClient, "terraform-runs")
	signal := traits.CancelSignal{
		RequestedBy: traits.UserID("user_456"),
		Reason:      "superseded by a newer run",
	}

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		signal,
	)
	if err != nil {
		t.Fatalf("CancelTemplateRun returned error: %v", err)
	}

	if workflowClient.signalWorkflowID != "template-run/tenant_123/run_123" {
		t.Fatalf("signal workflow ID = %q", workflowClient.signalWorkflowID)
	}
	if workflowClient.signalRunID != "" {
		t.Fatalf("signal run ID = %q, want empty", workflowClient.signalRunID)
	}
	if workflowClient.signalName != traits.CancelSignalName {
		t.Fatalf("signal name = %q", workflowClient.signalName)
	}
	if !reflect.DeepEqual(workflowClient.signalArg, signal) {
		t.Fatalf("signal arg = %#v, want %#v", workflowClient.signalArg, signal)
	}
}

func TestStartTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{executeErr: clientErr}, "terraform-runs")

	err := dispatcher.StartTemplateRun(context.Background(), traits.TemplateRunWorkflowInput{
		RunID:    traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	})
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "start template run workflow") {
		t.Fatalf("error = %q, want start context", err.Error())
	}
}

func TestApproveTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: clientErr}, "terraform-runs")

	err := dispatcher.ApproveTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		traits.ApprovalSignal{ApprovedBy: traits.UserID("user_123")},
	)
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "signal template run approval") {
		t.Fatalf("error = %q, want approval context", err.Error())
	}
}

func TestCancelTemplateRunWrapsClientError(t *testing.T) {
	t.Parallel()

	clientErr := errors.New("temporal unavailable")
	dispatcher := newDispatcher(&recordingWorkflowClient{signalErr: clientErr}, "terraform-runs")

	err := dispatcher.CancelTemplateRun(
		context.Background(),
		traits.TenantID("tenant_123"),
		traits.TemplateRunID("run_123"),
		traits.CancelSignal{RequestedBy: traits.UserID("user_456")},
	)
	if !errors.Is(err, clientErr) {
		t.Fatalf("error = %v, want wrapped client error", err)
	}
	if !strings.Contains(err.Error(), "signal template run cancellation") {
		t.Fatalf("error = %q, want cancellation context", err.Error())
	}
}

func TestTemplateRunWorkflowID(t *testing.T) {
	t.Parallel()

	got := templateRunWorkflowID(traits.TenantID("tenant_123"), traits.TemplateRunID("run_123"))
	if got != "template-run/tenant_123/run_123" {
		t.Fatalf("workflow ID = %q", got)
	}
}

type recordingWorkflowClient struct {
	executeOptions  client.StartWorkflowOptions
	executeWorkflow interface{}
	executeArgs     []interface{}
	executeErr      error
	signalWorkflowID string
	signalRunID      string
	signalName       string
	signalArg        interface{}
	signalErr        error
}

func (workflowClient *recordingWorkflowClient) ExecuteWorkflow(
	_ context.Context,
	options client.StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (client.WorkflowRun, error) {
	workflowClient.executeOptions = options
	workflowClient.executeWorkflow = workflow
	workflowClient.executeArgs = args
	return nil, workflowClient.executeErr
}

func (workflowClient *recordingWorkflowClient) SignalWorkflow(
	_ context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) error {
	workflowClient.signalWorkflowID = workflowID
	workflowClient.signalRunID = runID
	workflowClient.signalName = signalName
	workflowClient.signalArg = arg
	return workflowClient.signalErr
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/temporal
```

Expected: FAIL because dispatcher methods return raw client errors without operation context.

- [ ] **Step 3: Wrap dispatcher errors**

Replace `internal/temporal/dispatcher.go` with:

```go
package temporal

import (
	"context"
	"fmt"

	"github.com/vishu42/tflive/internal/traits"
	"go.temporal.io/sdk/client"
)

type workflowClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	SignalWorkflow(context.Context, string, string, string, interface{}) error
}

type Dispatcher struct {
	client    workflowClient
	taskQueue string
}

func NewDispatcher(temporalClient client.Client, taskQueue string) *Dispatcher {
	return newDispatcher(temporalClient, taskQueue)
}

func newDispatcher(temporalClient workflowClient, taskQueue string) *Dispatcher {
	return &Dispatcher{
		client:    temporalClient,
		taskQueue: taskQueue,
	}
}

func (dispatcher *Dispatcher) StartTemplateRun(ctx context.Context, input traits.TemplateRunWorkflowInput) error {
	_, err := dispatcher.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        templateRunWorkflowID(input.TenantID, input.RunID),
		TaskQueue: dispatcher.taskQueue,
	}, traits.TemplateRunWorkflowName, input)
	if err != nil {
		return fmt.Errorf("start template run workflow: %w", err)
	}

	return nil
}

func (dispatcher *Dispatcher) ApproveTemplateRun(
	ctx context.Context,
	tenantID traits.TenantID,
	runID traits.TemplateRunID,
	signal traits.ApprovalSignal,
) error {
	if err := dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		traits.ApprovalSignalName,
		signal,
	); err != nil {
		return fmt.Errorf("signal template run approval: %w", err)
	}

	return nil
}

func (dispatcher *Dispatcher) CancelTemplateRun(
	ctx context.Context,
	tenantID traits.TenantID,
	runID traits.TemplateRunID,
	signal traits.CancelSignal,
) error {
	if err := dispatcher.client.SignalWorkflow(
		ctx,
		templateRunWorkflowID(tenantID, runID),
		"",
		traits.CancelSignalName,
		signal,
	); err != nil {
		return fmt.Errorf("signal template run cancellation: %w", err)
	}

	return nil
}

func templateRunWorkflowID(tenantID traits.TenantID, runID traits.TemplateRunID) string {
	return fmt.Sprintf("template-run/%s/%s", tenantID, runID)
}
```

- [ ] **Step 4: Run the focused test**

Run:

```bash
go test ./internal/temporal
```

Expected: PASS.

- [ ] **Step 5: Commit the error wrapping slice**

Run:

```bash
git add internal/temporal/dispatcher.go internal/temporal/dispatcher_test.go
git commit -m "test: cover temporal dispatcher errors"
```

Expected: commit succeeds with only dispatcher files staged.

### Task 5: Verify Whole Repository

**Files:**
- Modify: Go files touched by gofmt if formatting changed.

- [ ] **Step 1: Format Temporal package**

Run:

```bash
gofmt -w internal/temporal
```

Expected: no output.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/temporal
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS for all packages. Postgres integration tests skip when `tflive_POSTGRES_TEST_DSN` is not set.

- [ ] **Step 4: Check git status**

Run:

```bash
git status --short
```

Expected: no unstaged changes from this Temporal implementation except pre-existing untracked files unrelated to this work.
