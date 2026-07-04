# File Logsink Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist Terraform command output to local per-run phase log files.

**Architecture:** Add a focused `internal/logsink.FileSink` for local phase files. Extend `runner.TerraformCommand` with optional stdout/stderr writers while preserving stdout/stderr defaults. Wire the worker activity's local Terraform runner to open a phase file for each command and pass it into the runner.

**Tech Stack:** Go standard library, existing `internal/traits`, existing unit test style with `go test`.

---

### Task 1: File Sink

**Files:**
- Create: `internal/logsink/filesink_test.go`
- Create: `internal/logsink/filesink.go`

- [ ] Write tests for opening a phase log, appending output, rejecting unsafe phases, and mapping Terraform commands to phases.
- [ ] Run `go test ./internal/logsink` and confirm tests fail because the file sink API does not exist.
- [ ] Implement `FileSink`, `OpenPhase`, and `PhaseForTerraformCommand`.
- [ ] Run `go test ./internal/logsink` and confirm tests pass.

### Task 2: Runner Output Plumbing

**Files:**
- Modify: `internal/runner/terraform_test.go`
- Modify: `internal/runner/terraform.go`

- [ ] Write a test proving `LocalProcessRunner` passes configured stdout/stderr writers to the command executor.
- [ ] Run `go test ./internal/runner` and confirm the new test fails.
- [ ] Extend `TerraformCommand` and `CommandExecutor` to carry stdout/stderr writers.
- [ ] Run `go test ./internal/runner` and confirm tests pass.

### Task 3: Activity Wiring

**Files:**
- Modify: `internal/activities/template_run_test.go`
- Modify: `internal/activities/template_run.go`

- [ ] Write a test proving the local Terraform activity writes command output to `<workspace>/logs/plan.log`.
- [ ] Run `go test ./internal/activities` and confirm the new test fails.
- [ ] Wire `localTerraformRunner` to open a `logsink.FileSink` phase file and pass it to the runner.
- [ ] Run `go test ./internal/activities` and confirm tests pass.

### Task 4: Full Verification

**Files:**
- Modify only files above.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./...`.
- [ ] Review `git diff --stat` and `git diff` for unintended changes.
