# Megagega Terraform Platform

> [!WARNING]
> Megagega is not production ready. It is an MVP baseline intended for local
> development, evaluation, and continued hardening.

## Overview

Megagega composes infrastructure stacks from reusable Terraform templates, uses Temporal for durable orchestration and Postgres for product state, persists logs and artifacts, and runs OpenTofu operations in worker processes.

## Architecture

```text
UI -> API -> Postgres (product state + workflow outbox)
                         |
                         v
                  Outbox Dispatcher -> Temporal -> Workers -> OpenTofu
```

Template-run creation atomically commits the run and workflow-start intent in Postgres. The worker-hosted dispatcher delivers that intent at least once, while deterministic Temporal workflow IDs make repeated starts idempotent. See [Architecture and product model](docs/architecture.md) for leases, retries, workflows, and component responsibilities.

## Local Development

Run the backend API and the Vite UI as separate processes.

```text
API: http://localhost:8081
UI:  http://localhost:5173
```

Start backend dependencies:

```bash
docker compose up app-postgres temporal-postgres temporal temporal-ui minio minio-init
```

The local MinIO API is available at `http://localhost:9000`, and the console is
available at `http://localhost:9001`. Credentials and the bucket name are loaded
from `.env`.

Start the API with the local environment:

```bash
set -a
source .env
set +a
go run ./cmd/megagega-api
```

Start the worker in another shell:

```bash
set -a
source .env
set +a
go run ./cmd/megagega-worker
```

Start the UI:

```bash
cd web
npm install
npm run dev
```

The Vite dev server proxies `/v1/*` and `/healthz` to the Go API.

## Repository Layout

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

## Documentation

- [Architecture and product model](docs/architecture.md)
- [Transactional workflow-outbox design](docs/superpowers/specs/2026-07-10-template-run-workflow-outbox-design.md)
