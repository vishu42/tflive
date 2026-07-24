# Run History & Cross-User Approval Visibility

**Date:** 2026-07-25
**Status:** Draft

## Overview

Fix a bug where a template run's plan/apply/approval state is only visible in the browser tab that started it. A second user (even the stack owner, even one who holds `canApprove`) opening `/stacks/:stackId/runs` sees "not started" for a run someone else kicked off, so a pending `waiting_approval` apply never surfaces an Approve button for them. Refreshing the page loses the same information even for the original user — there is no run history anywhere in the UI.

## Root Cause

`template_runs` already has an index for exactly this access pattern (`template_runs_tenant_id_stack_template_id_idx`, [migrations/0001_app_repositories.sql](internal/postgres/migrations/0001_app_repositories.sql)), but no layer of the stack ever queries "list runs for this stack template": no repository method, no service method, no route, no client function. [`RunsListRow.tsx`](web/src/features/runs/RunsListRow.tsx) instead tracks the current plan/apply run in local React state (`useState("")`), populated only from the response of the mutation that started it in that session. This is a discovery gap, not a permissions gap — `RunDetailScreen`'s Approve button is already correctly gated by server-computed `effectiveCapabilities` ([useStackCapabilities.ts](web/src/auth/useStackCapabilities.ts)); a user just never learns a run ID exists to navigate to.

## Scope

- Full run history per stack template (all runs, most recent first), replacing the "not started" placeholder.
- Passive visibility only: the approver sees the pending run next time they open the Runs screen or a run detail page. No push notifications, no badges, no email.
- No changes to the approval permission model itself (`authz.PermissionApprove`, `authz.PermissionView`) — this only fixes discovery of runs that already exist.

## Design

### Backend

**Repository — `internal/postgres/repositories.go`**

Add `ListTemplateRuns(ctx, tenantID, stackTemplateID) ([]traits.TemplateRun, error)`, modeled on the existing `ListTemplateRunLogs` (same file, ~line 1104):

```go
func (store *Store) ListTemplateRuns(ctx context.Context, tenantID traits.TenantID, stackTemplateID traits.StackTemplateID) ([]traits.TemplateRun, error) {
	rows, err := store.pool.Query(ctx, `
		select id, tenant_id, stack_template_id, template_revision_id, source_template_id,
			operation, selected_ref, resolved_commit_sha, workspace_name, config_json,
			backend_type, backend_config_hash, status, trigger_actor,
			started_at, completed_at, error_summary
		from template_runs
		where tenant_id = $1 and stack_template_id = $2
		order by started_at desc nulls last, id desc
	`, tenantID, stackTemplateID)
	...
}
```

Scans into `[]traits.TemplateRun` the same way `GetTemplateRun` does per-row.

**Service — `internal/app/service.go`**

```go
type ListTemplateRunsCommand struct {
	TenantID        traits.TenantID
	StackTemplateID traits.StackTemplateID
}

func (service *Service) ListTemplateRuns(ctx context.Context, command ListTemplateRunsCommand) ([]traits.TemplateRun, error) {
	if err := validateListTemplateRunsCommand(command); err != nil {
		return nil, err
	}
	if _, err := service.authorizedStackTemplate(ctx, command.TenantID, command.StackTemplateID, authz.PermissionView, ErrNotFound); err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	runs, err := service.TemplateRuns.ListTemplateRuns(ctx, command.TenantID, command.StackTemplateID)
	if err != nil {
		return nil, fmt.Errorf("list template runs: %w", err)
	}
	if runs == nil {
		return []traits.TemplateRun{}, nil
	}
	return runs, nil
}
```

Same permission (`authz.PermissionView`) and `authorizedStackTemplate` helper already used by `StartTemplateRun` (with `PermissionOperate`) — this reuses the existing authorization seam at [authorization.go:158](internal/app/authorization.go#L158), so anyone who can already view the stack/its runs individually can now see the list too. Add `ListTemplateRuns` to the `TemplateRunRepository` interface in service.go alongside the existing `ApproveTemplateRun`/`RequestTemplateRunCancellation` methods.

**API — `internal/api/server.go`**

Register alongside the existing `POST` on the same path (Go 1.22 mux supports distinct verbs on one pattern):

```go
// Lists runs for an installed stack template, most recent first.
server.handleTenantRoute("GET /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs", server.handleListTemplateRuns)
```

```go
func (server *Server) handleListTemplateRuns(response http.ResponseWriter, request *http.Request) {
	runs, err := server.service.ListTemplateRuns(request.Context(), app.ListTemplateRunsCommand{
		TenantID:        traits.TenantID(request.PathValue("tenant_id")),
		StackTemplateID: traits.StackTemplateID(request.PathValue("stack_template_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, runs)
}
```

### Frontend

**`web/src/api/client.ts`**

```ts
export function listTemplateRuns(tenantID: string, stackTemplateID: string): Promise<TemplateRun[]> {
  return requestJSON(`/v1/tenants/${encodeURIComponent(tenantID)}/stack-templates/${encodeURIComponent(stackTemplateID)}/runs`);
}
```

**`web/src/api/queryKeys.ts`**

```ts
templateRuns: (tenantID: string, stackTemplateID: string) => ["templateRuns", tenantID, stackTemplateID] as const,
```

**`web/src/api/queries.ts`**

```ts
export function useTemplateRunsQuery(tenantID: string, stackTemplateID: string) {
  return useQuery({
    queryKey: queryKeys.templateRuns(tenantID, stackTemplateID),
    queryFn: () => client.listTemplateRuns(tenantID, stackTemplateID),
    enabled: stackTemplateID !== "",
    refetchInterval: POLL_INTERVAL_MS
  });
}
```

Always polls (unconditionally, unlike per-run polling which stops at terminal status) since the list's own terminal-ness is ambiguous — it's cheap, tenant-scoped, and matches the existing 1.5s interval used elsewhere.

**`web/src/features/runs/RunsListRow.tsx`**

Replace `useState`-tracked `planRunID`/`applyRunID` with derivation from the fetched list:

```ts
const runsQuery = useTemplateRunsQuery(tenantID, stackTemplate.id);
const runs = runsQuery.data ?? [];
const planRun = latestRunOf(runs, "plan");
const applyRun = latestRunOf(runs, "apply");
```

where `latestRunOf(runs, operation)` picks the first matching entry (list is already ordered most-recent-first by the API). `handlePlan`/`handleApply` keep their existing `mutateAsync` calls but their `onSuccess` now also invalidates `queryKeys.templateRuns(tenantID, stackTemplate.id)` (in addition to the existing per-run cache seed) so a newly started run appears in the actor's own view immediately, and within one poll interval for anyone else with the screen open.

Below the existing Plan/Apply/Approve/Cancel action row, render the history list: one line per past run — operation, status, `trigger_actor`, started/completed timestamps — each linking to `/stacks/:stackId/runs/:runId`, using the existing `StatusRow` component and `.panel`/`.muted` styling (no new visual system, matching the [create-stack-ui-design.md](docs/superpowers/specs/2026-07-20-create-stack-ui-design.md) convention of reusing existing CSS).

**`RunDetailScreen.tsx`** — no changes needed. It already fetches by run ID and gates Approve/Cancel via `RequireCapability`, so once a user can reach a run through the new history list, their existing capability-based button correctly appears.

## Testing

- Repository: `ListTemplateRuns` returns runs scoped to `(tenant_id, stack_template_id)`, ordered most-recent-first, empty slice (not nil) when none exist.
- Service: `TestListTemplateRunsReturnsRunsForStackTemplate`, `TestListTemplateRunsDeniesWithoutViewPermission` — mirroring existing `TestListTemplateRevisionsReturnsTenantTemplateRevisions` / `TestApproveRunCallsService` patterns.
- API: `TestListTemplateRunsCallsService`, plus a route-registration smoke test.
- Frontend: update `RunsListRow.test.tsx` to seed `useTemplateRunsQuery` data instead of relying on mutation-response state; add a case where a run is already `waiting_approval` in the fetched list on initial mount (i.e., simulating "a different user started this run") and assert the Approve button renders enabled for a capability-holding viewer.

## Files

| File | Action |
|------|--------|
| `internal/postgres/repositories.go` | Add `ListTemplateRuns` |
| `internal/postgres/store_test.go` | Add coverage |
| `internal/app/service.go` | Add `ListTemplateRunsCommand`, `ListTemplateRuns`, `validateListTemplateRunsCommand`, extend `TemplateRunRepository` interface |
| `internal/app/service_test.go` | Add coverage |
| `internal/api/server.go` | Register route, add `handleListTemplateRuns` |
| `internal/api/server_test.go` | Add coverage |
| `web/src/api/types.ts` | No change (reuses `TemplateRun`) |
| `web/src/api/client.ts` | Add `listTemplateRuns` |
| `web/src/api/queryKeys.ts` | Add `templateRuns` key |
| `web/src/api/queries.ts` | Add `useTemplateRunsQuery`; extend mutation `onSuccess` handlers |
| `web/src/features/runs/RunsListRow.tsx` | Replace local state with server-derived data; add history list |
| `web/src/features/runs/RunsListRow.test.tsx` | Update/add coverage |
