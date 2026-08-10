# Role Intent Coalescing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans (inline execution selected for this task). Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ensure asynchronous stack-role mutations always enqueue the latest requested desired state, even when OpenFGA still reports an older state.

**Architecture:** Keep OpenFGA as the source of the current state and the durable reconcile queue as the source of the latest desired state. Remove service-layer no-op returns that can hide a pending intent; the existing queue key and revision coalescing will replace the pending payload.

**Tech Stack:** Go 1.24, standard `testing` package, existing application service and queue test doubles.

## Global Constraints

- Preserve authorization and last-owner validation behavior.
- Keep role mutations asynchronous; do not add synchronous OpenFGA writes.
- Use the existing `InTx` audit-plus-enqueue path.
- Add regression coverage in `internal/app/service_test.go` before changing production code.

---

### Task 1: Add regression tests for stale OpenFGA reads

**Files:**
- Modify: `internal/app/service_test.go`

**Interfaces:**
- Consumes: `Service.AssignStackRole`, `Service.RevokeStackRole`, `recordingAuthorizer`, and `recordingUnitOfWork`.
- Produces: tests proving an already-matching or currently-absent OpenFGA grant still creates the desired queue intent.

- [x] **Step 1: Write the failing tests**

Add one test where `recordingAuthorizer` reports no grant and `RevokeStackRole` must enqueue an empty-role payload. Add another where the authorizer reports the requested role and `AssignStackRole` must still enqueue that desired role.

- [x] **Step 2: Run the focused tests to verify they fail**

Run:

```bash
rtk /opt/homebrew/bin/go test ./internal/app -run 'Test(RevokeStackRoleEnqueuesEmptyRoleWhenGrantIsAbsent|AssignStackRoleEnqueuesMatchingRole)' -count=1
```

Expected: both tests fail because the current service returns before entering the transaction.

### Task 2: Enqueue every latest desired role

**Files:**
- Modify: `internal/app/service.go`

**Interfaces:**
- Consumes: the existing current-grant and last-owner checks.
- Produces: an audit event and reconcile queue request for every valid desired role, including empty role and same-role requests.

- [x] **Step 1: Remove only the stale-state early returns**

Delete the `currentRole == command.Role` return from `AssignStackRole` and the `targetRole == ""` return from `RevokeStackRole`. Leave role validation and last-owner protection unchanged.

- [x] **Step 2: Run the regression tests to verify they pass**

Run:

```bash
rtk /opt/homebrew/bin/go test ./internal/app -run 'Test(RevokeStackRoleEnqueuesEmptyRoleWhenGrantIsAbsent|AssignStackRoleEnqueuesMatchingRole)' -count=1
```

Expected: PASS.

### Task 3: Verify the affected packages

**Files:**
- No additional files.

- [x] **Step 1: Run focused application tests**

```bash
rtk /opt/homebrew/bin/go test ./internal/app
```

- [x] **Step 2: Run related queue, persistence, API, and worker tests**

```bash
rtk /opt/homebrew/bin/go test ./internal/queue ./internal/postgres ./internal/app ./cmd/api ./cmd/worker
```

- [x] **Step 3: Run static and whitespace checks**

```bash
rtk /opt/homebrew/bin/go vet ./internal/queue ./internal/postgres ./internal/app ./cmd/api ./cmd/worker
rtk git diff --check
```
