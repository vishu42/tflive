# Run Read And Log API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let clients fetch one `TemplateRun` and its persisted local phase logs.

**Architecture:** Add app read interfaces and use cases, implement tenant-scoped Postgres run lookup, add a local log reader in `internal/logsink`, and expose two HTTP GET endpoints. Keep log storage behind an app interface so object storage can replace local files later.

**Tech Stack:** Go standard library, pgx, existing `internal/app`, `internal/api`, `internal/postgres`, `internal/logsink`, and `go test`.

---

### Task 1: App Read Use Cases

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`

- [ ] Write failing tests for `GetTemplateRun` and `GetTemplateRunLog`.
- [ ] Run `go test ./internal/app` and confirm the new tests fail because read APIs do not exist.
- [ ] Add `ErrNotFound`, `TemplateRunReader`, `TemplateRunLogReader`, `GetTemplateRunCommand`, `GetTemplateRunLogCommand`, and service methods.
- [ ] Run `go test ./internal/app` and confirm tests pass.

### Task 2: Postgres Run Lookup

**Files:**
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`

- [ ] Write failing tests for tenant-scoped `GetTemplateRun`.
- [ ] Run `go test ./internal/postgres` and confirm the test fails because the method does not exist.
- [ ] Implement `GetTemplateRun`.
- [ ] Run `go test ./internal/postgres` and confirm tests pass.

### Task 3: Local Log Reader

**Files:**
- Modify: `internal/logsink/filesink.go`
- Modify: `internal/logsink/filesink_test.go`

- [ ] Write failing tests for reading a phase log, rejecting unsafe path components, and missing logs.
- [ ] Run `go test ./internal/logsink` and confirm the tests fail.
- [ ] Implement `LocalReader` or equivalent focused reader.
- [ ] Run `go test ./internal/logsink` and confirm tests pass.

### Task 4: API Endpoints And Wiring

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `cmd/tflive-api/main.go`
- Modify: `cmd/tflive-api/main_test.go`

- [ ] Write failing API tests for run JSON, log text, invalid phase, and missing resources.
- [ ] Run `go test ./internal/api ./cmd/tflive-api` and confirm tests fail.
- [ ] Add routes, handlers, response mapping, and wire the local log reader from API config run root.
- [ ] Run `go test ./internal/api ./cmd/tflive-api` and confirm tests pass.

### Task 5: Full Verification

**Files:**
- Modify only files above.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./...`.
- [ ] Review `git diff --stat` and `git diff` for unintended changes.
