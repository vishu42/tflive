# internal/temporal API Dispatch Design

## Goal

Build the first useful `internal/temporal` slice as the API-side Temporal client adapter. The package will implement the app-owned `app.WorkflowDispatcher` interface so API use cases can start template run workflows and send approval or cancellation signals without depending on Temporal SDK types.

## Scope

This slice includes:

- Temporal SDK dependency setup.
- A small Temporal client configuration and dial helper.
- A `Dispatcher` type that implements `app.WorkflowDispatcher`.
- Stable workflow ID construction for template runs.
- Unit tests around workflow start and signal dispatch behavior.

This slice does not include worker registration helpers, `cmd/megagega-worker` wiring, workflow implementations, activity implementations, API startup wiring, or integration tests against a live Temporal server. Those belong in later slices once `internal/workflows` and `internal/activities` contain real executable behavior.

## Package Shape

`internal/temporal` exposes a narrow adapter API:

```go
type Config struct {
	Address   string
	Namespace string
}

func Dial(ctx context.Context, cfg Config) (client.Client, error)

type Dispatcher struct {
	client    workflowClient
	taskQueue string
}

func NewDispatcher(client client.Client, taskQueue string) *Dispatcher
```

The public constructor accepts the Temporal SDK client. Internally, tests can use a small package-owned `workflowClient` interface that includes only the SDK methods the dispatcher needs. This keeps tests fast and focused without requiring a live Temporal server.

`Dispatcher` may depend on `internal/app` for compile-time interface assertions and `internal/traits` for workflow names, signal names, and payloads. `internal/app` will not depend on `internal/temporal`.

## Configuration

`Config.Address` is required by `Dial`. `Config.Namespace` is optional and defaults to Temporal's `default` namespace when empty.

The task queue is intentionally not part of `Dial`; it belongs to dispatcher construction because the same client can support multiple dispatchers or task queues in the future. For the MVP, callers should pass the shared Terraform run queue:

```text
terraform-runs
```

This slice does not modify `internal/config` yet. Command-level wiring can add environment loading for Temporal address, namespace, and task queue in a later API wiring slice.

## Workflow IDs

Template run workflow IDs are stable and derived from tenant ID and run ID:

```text
template-run/<tenant-id>/<run-id>
```

This makes Temporal UI inspection readable and gives repeated API attempts a deterministic target. The first implementation will not add a separate idempotency policy beyond the workflow ID because the app service already persists the run before dispatch and the API retry behavior is not designed yet.

## Dispatch Behavior

`StartTemplateRun` calls Temporal `ExecuteWorkflow` with:

- workflow name: `traits.TemplateRunWorkflowName`
- workflow ID: `template-run/<tenant-id>/<run-id>`
- task queue: the dispatcher's configured task queue
- input: the provided `traits.TemplateRunWorkflowInput`

The method only starts the workflow. It does not wait for workflow completion.

`ApproveTemplateRun` calls Temporal `SignalWorkflow` with:

- workflow ID: `template-run/<tenant-id>/<run-id>`
- run ID: empty, so Temporal targets the currently open execution for that workflow ID
- signal name: `traits.ApprovalSignalName`
- payload: the provided `traits.ApprovalSignal`

`CancelTemplateRun` follows the same shape with `traits.CancelSignalName` and `traits.CancelSignal`.

## Error Handling

`Dial` validates required configuration before calling the SDK and wraps SDK dial failures with operation context.

Dispatcher methods wrap SDK errors with operation context:

- `start template run workflow`
- `signal template run approval`
- `signal template run cancellation`

The dispatcher will not translate Temporal errors into app domain errors in this slice. Domain eligibility decisions remain in `internal/app` and persistence adapters.

## Testing

Tests will follow TDD and stay unit-level:

- `Dispatcher` satisfies `app.WorkflowDispatcher`.
- `StartTemplateRun` records the expected workflow name, workflow ID, task queue, and input.
- `ApproveTemplateRun` records the expected workflow ID, signal name, and approval payload.
- `CancelTemplateRun` records the expected workflow ID, signal name, and cancellation payload.
- Each dispatcher method wraps client errors with useful operation context.

No Temporal server is required for these tests. The fake client implements the package-owned `workflowClient` interface and records calls.

## Dependencies

Use the official Temporal Go SDK package:

```text
go.temporal.io/sdk
```

No workflow testing framework is introduced in this slice because this package only dispatches workflows and signals. Workflow behavior tests will belong to `internal/workflows`.
