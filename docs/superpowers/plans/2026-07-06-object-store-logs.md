# Object Store Logs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store completed Terraform phase logs in an object-store boundary instead of requiring API and worker to share local disk.

**Architecture:** Add `internal/artifacts` object store adapters, make the worker upload phase logs after each Terraform command, and make the API read logs through the artifact store. Keep local files as temporary worker spool.

**Tech Stack:** Go standard library, HTTP/SigV4, existing app/config/activity boundaries, `go test`.

---

### Task 1: Artifact Store

**Files:**
- Create: `internal/artifacts/store.go`
- Create: `internal/artifacts/store_test.go`

- [ ] Add failing tests for log key construction, filesystem put/get, missing objects, and unsafe keys.
- [ ] Implement `ObjectStore`, `LogStore`, filesystem adapter, and a minimal S3 adapter.
- [ ] Run `go test ./internal/artifacts`.

### Task 2: Config And Wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/tflive-api/main.go`
- Modify: `cmd/tflive-api/main_test.go`
- Modify: `cmd/tflive-worker/main.go`
- Modify: `cmd/tflive-worker/main_test.go`

- [ ] Add artifact-store config tests.
- [ ] Wire both API and worker to the same object-store factory.
- [ ] Run command package and config tests.

### Task 3: Activity Upload

**Files:**
- Modify: `internal/activities/template_run.go`
- Modify: `internal/activities/template_run_test.go`

- [ ] Add failing test that local Terraform runner uploads `plan.log` to artifact storage after command completion.
- [ ] Implement upload after closing the local phase log.
- [ ] Run `go test ./internal/activities`.

### Task 4: Full Verification

**Files:**
- Modify only files above.

- [ ] Run `gofmt`.
- [ ] Run `git diff --check`.
- [ ] Run `go test ./...`.
