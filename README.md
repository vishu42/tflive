# tflive Terraform Platform

> [!WARNING]
> tflive is not production ready. It is an MVP baseline intended for local
> development, evaluation, and continued hardening.

## Overview

tflive composes infrastructure stacks from reusable Terraform templates, uses Temporal for durable orchestration and Postgres for product state, persists logs and artifacts, and runs OpenTofu operations in worker processes.

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

On a clean checkout, copy the explicitly local-development environment example
once:

```bash
cp .env.example .env
```

Provision OpenFGA in two phases. Bootstrap prints two environment assignments;
copy those lines into `.env` as text, and do not execute or evaluate the
bootstrap output directly:

```bash
docker compose --env-file .env.example up -d openfga-postgres openfga-migrate openfga
docker compose --env-file .env.example run --rm openfga-provision bootstrap
# Copy OPENFGA_STORE_ID and OPENFGA_MODEL_ID from stdout into .env.
docker compose run --rm openfga-provision verify
```

Run only one bootstrap at a time because OpenFGA store names are not unique.
Bootstrap fails closed if the `tflive` store name or semantic model match is
ambiguous, and it can be retried safely after a partial store-only or model-only
failure. Verify uses the exact `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` copied
into `.env`; it never mutates OpenFGA and never substitutes the latest model.
The API will later consume these same explicit IDs. See
[Authentication and authorization](docs/authentication.md) for the role matrix,
immutable-model update procedure, and recovery details.

After verify succeeds, start the complete dependency stack:

```bash
docker compose up app-postgres keycloak-postgres keycloak keycloak-provision openfga-postgres openfga-migrate openfga temporal-postgres temporal temporal-ui minio minio-init
```

Keycloak is available at `http://localhost:8082`. The one-shot
`keycloak-provision` service creates or reconciles the `tflive` realm after
Keycloak is healthy and must exit with code `0`. Both the master bootstrap and
tflive platform-administrator credentials come from `.env` and are for local
development only. See [Keycloak authentication](docs/authentication.md) for
the exact clients, claims, roles, administrator boundary, and safe reruns.

The OpenFGA HTTP API is available at `http://localhost:8083`, and its gRPC API
is available at `localhost:8084`. AUTH-004 provisions the tflive store and
authorization model after OpenFGA is healthy.

The local MinIO API is available at `http://localhost:9000`, and the console is
available at `http://localhost:9001`. Credentials and the bucket name are loaded
from `.env`.

Start the API with the local environment:

```bash
set -a
source .env
set +a
go run ./cmd/tflive-api
```

Start the worker in another shell:

```bash
set -a
source .env
set +a
go run ./cmd/tflive-worker
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
- [Authentication and authorization](docs/authentication.md)
- [Transactional workflow-outbox design](docs/superpowers/specs/2026-07-10-template-run-workflow-outbox-design.md)
