# Local State Hydration and MinIO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load existing stacks/templates from the DB-backed API and run local artifact storage through MinIO.

**Architecture:** Add tenant-scoped list methods to the app service and Postgres store, expose them through HTTP, then make the React UI hydrate from those endpoints. Docker Compose gains MinIO plus a one-shot bucket initializer, and local env files point the existing S3 adapter at MinIO.

**Tech Stack:** Go HTTP API, Postgres repositories with pgx, React/Vite/TypeScript, Docker Compose, MinIO S3-compatible storage.

---

### Task 1: Backend List APIs

**Files:**
- Modify: `internal/app/service.go`
- Modify: `internal/app/service_test.go`
- Modify: `internal/postgres/repositories.go`
- Modify: `internal/postgres/store_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

- [ ] Add failing app service tests for `ListStacks` and `ListTemplates`.
- [ ] Add failing Postgres tests for tenant-scoped list queries.
- [ ] Add failing API tests for `GET /v1/tenants/{tenant_id}/stacks` and `GET /v1/tenants/{tenant_id}/templates`.
- [ ] Implement repository interfaces, service methods, Postgres queries, routes, and response DTOs.
- [ ] Run `go test ./internal/app ./internal/postgres ./internal/api`.

### Task 2: MinIO Local Runtime

**Files:**
- Modify: `docker-compose.yaml`
- Modify: `.env`
- Modify: `.env.example`
- Modify: `README.md`

- [ ] Add `minio` and `minio-init` services to Compose.
- [ ] Add `minio-data` volume.
- [ ] Set local artifact env vars to S3/MinIO values.
- [ ] Update local development docs to include MinIO.
- [ ] Validate `docker compose config`.

### Task 3: UI API Client and Hydration

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`

- [ ] Add failing client tests for `listStacks` and `listTemplates`.
- [ ] Add client functions and types for stack/template lists.
- [ ] Add UI state for loaded stacks/templates and selected IDs.
- [ ] Load lists on mount and tenant changes.
- [ ] Select created stacks and completed registration templates after refresh.
- [ ] Load stack details and template variables from selected menu entries.
- [ ] Run `cd web && npm test` and `cd web && npm run build`.

### Task 4: Final Verification

**Files:**
- All changed files.

- [ ] Run `go test ./...`.
- [ ] Run `cd web && npm test`.
- [ ] Run `cd web && npm run build`.
- [ ] Confirm `git status --short`.
- [ ] Commit the completed changes.
