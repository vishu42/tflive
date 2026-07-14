# README Documentation Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the oversized root README with a concise onboarding guide and preserve the detailed product/system design in `docs/architecture.md`.

**Architecture:** The root README becomes the stable entry point for understanding and running tflive. `docs/architecture.md` becomes the single detailed current-state architecture reference; historical implementation specs and plans remain under `docs/superpowers/`.

**Tech Stack:** GitHub-flavored Markdown, Go project commands, relative repository links.

## Global Constraints

- Preserve the MVP warning and local startup commands in `README.md`.
- Keep the transactional-outbox summary accurate: run state and intent commit atomically in Postgres, dispatch is at least once, and deterministic Temporal workflow IDs make repeated starts idempotent.
- Do not duplicate detailed domain models, workflow state sequences, security guidance, persistence inventories, scaling guidance, or deferred design questions in the README.
- Do not modify application source code.
- Keep historical files under `docs/superpowers/` unchanged.

---

### Task 1: Create the Canonical Architecture Document

**Files:**
- Create: `docs/architecture.md`
- Source: `README.md`

**Interfaces:**
- Consumes: the detailed architecture and product-design sections currently in `README.md`.
- Produces: `docs/architecture.md`, the canonical detailed current-state design document linked by the README.

- [ ] **Step 1: Create the architecture document with an explicit purpose**

Start `docs/architecture.md` with:

```markdown
# tflive Architecture

This document describes tflive's current MVP product model, system architecture, execution workflows, persistence boundaries, security posture, and deferred design topics. For setup and local development, see the [project README](../README.md).
```

- [ ] **Step 2: Move the detailed design sections into the document**

Preserve and order the current README material under these headings:

```text
Goals
Non-Goals For MVP
Architecture
Core Boundaries
Major Components
Go Package Layout
Template Model
Template Registration Workflow
Stack Model
StackTemplate Model
Terraform Backend
Inputs And Secrets
Credential Sets
GitHub Integration
Reliable Template Run Dispatch
TemplateRun Workflow
Approval
Logs
Activity Logs
Persistence
Scaling
Deferred Design Topics
MVP Summary
```

Do not copy `Local UI Development`; it remains in the root README. Retain the architecture diagram, workflow state sequences, package ownership descriptions, outbox failure/retry semantics, and all deferred-topic subsections.

- [ ] **Step 3: Verify architecture coverage**

Run:

```bash
rg -n '^## (Goals|Non-Goals For MVP|Architecture|Core Boundaries|Major Components|Go Package Layout|Template Model|Template Registration Workflow|Stack Model|StackTemplate Model|Terraform Backend|Inputs And Secrets|Credential Sets|GitHub Integration|Reliable Template Run Dispatch|TemplateRun Workflow|Approval|Logs|Activity Logs|Persistence|Scaling|Deferred Design Topics|MVP Summary)$' docs/architecture.md
```

Expected: exactly 23 matching level-two headings, in the order listed above.

### Task 2: Reduce the README to Essentials

**Files:**
- Modify: `README.md`
- Consume: `docs/architecture.md`

**Interfaces:**
- Consumes: the architecture document created in Task 1.
- Produces: a concise repository entry point with setup commands and links, without duplicating detailed design material.

- [ ] **Step 1: Replace the README with the onboarding outline**

Use this level-two heading structure:

```markdown
## Overview
## Architecture
## Local Development
## Repository Layout
## Documentation
```

Retain the existing title and MVP warning above `Overview`.

Under `Overview`, keep one short paragraph describing reusable Terraform templates, stacks, Temporal orchestration, Postgres product state, persisted logs/artifacts, and worker-executed OpenTofu operations.

Under `Architecture`, include only this flow and a short explanation:

```text
UI -> API -> Postgres (product state + workflow outbox)
                         |
                         v
                  Outbox Dispatcher -> Temporal -> Workers -> OpenTofu
```

State that template-run creation atomically persists the run and workflow-start intent, while the worker-hosted dispatcher delivers that intent to Temporal. Link to `docs/architecture.md` for leases, retries, idempotency, workflows, and component responsibilities.

- [ ] **Step 2: Preserve runnable local-development commands**

Move the existing dependency, API, worker, and UI commands under `Local Development` without changing their command text:

```bash
docker compose up app-postgres temporal-postgres temporal temporal-ui minio minio-init
go run ./cmd/tflive-api
go run ./cmd/tflive-worker
cd web
npm install
npm run dev
```

Preserve the `set -a`, `source .env`, and `set +a` environment-loading lines around both Go commands. Preserve the API/UI and MinIO endpoint descriptions.

- [ ] **Step 3: Add a compact repository map and documentation links**

Under `Repository Layout`, describe only:

```text
cmd/                  API and worker entry points
internal/app/         application use cases and ports
internal/api/         HTTP transport
internal/postgres/    product persistence and workflow outbox
internal/dispatch/    Postgres-to-Temporal dispatch loop
internal/temporal/    Temporal client adapter
internal/workflows/   deterministic workflows
internal/activities/  side-effecting Temporal activities
internal/runner/      OpenTofu execution
web/                  Vite UI
```

Under `Documentation`, link to:

```markdown
- [Architecture and product model](docs/architecture.md)
- [Transactional workflow-outbox design](docs/superpowers/specs/2026-07-10-template-run-workflow-outbox-design.md)
```

- [ ] **Step 4: Verify README size and scope**

Run:

```bash
rg -n '^## ' README.md
wc -l README.md
```

Expected: exactly the five approved level-two headings and no more than 180 lines.

Run:

```bash
rg -n '^## (Template Model|TemplateRun Workflow|Deferred Design Topics|Persistence|Scaling)$' README.md
```

Expected: no matches.

### Task 3: Verify and Commit the Documentation Split

**Files:**
- Verify: `README.md`
- Verify: `docs/architecture.md`

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: a verified documentation-only commit.

- [ ] **Step 1: Verify relative link targets exist**

Run:

```bash
test -f docs/architecture.md
test -f docs/superpowers/specs/2026-07-10-template-run-workflow-outbox-design.md
```

Expected: both commands exit 0.

- [ ] **Step 2: Check Markdown diff quality**

Run:

```bash
git diff --check
git diff --stat
```

Expected: no whitespace errors; changes are limited to `README.md`, `docs/architecture.md`, and this plan.

- [ ] **Step 3: Run the repository test suite**

Run:

```bash
go test ./...
```

Expected: all Go packages pass or report no test files.

- [ ] **Step 4: Commit the documentation restructure**

Run:

```bash
git add README.md docs/architecture.md docs/superpowers/plans/2026-07-10-readme-documentation-split.md
git commit -m "docs: simplify readme and move architecture details"
```

Expected: one documentation-only commit on `feature-template-run-workflow-outbox`.
