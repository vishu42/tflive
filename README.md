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

## Quickstart

Requires Docker. No Go or Node toolchain, and no `.env`.

```bash
docker compose up
```

The UI is at `http://localhost:5173`, the API at `http://localhost:8081`, and
Keycloak at `http://tflive.localhost:8082`. Sign in with the platform
administrator credentials in `.env.example`.

First run builds the Go binaries and the Vite bundle, so it takes a few minutes;
later runs are cached. Provisioning is automatic and converges when re-run, so
`docker compose up` is safe to repeat.

Optional profiles, neither started by default:

```bash
docker compose --profile s3 up     # MinIO, for the S3 artifact adapter
docker compose --profile debug up  # Temporal UI on http://localhost:8080
```

### Upgrading an existing checkout

The four Postgres instances are now one server hosting five databases. Its init
script runs only against an empty data directory, so a checkout with volumes
from before this change needs a one-time reset. **This discards local
development data — the app database, the provisioned realm, the OpenFGA store,
and Temporal history.** All of it is rebuilt automatically on the next start.

```bash
docker compose down -v
docker compose up
```

If you keep a `.env`, update two values in it to match `.env.example`:

```bash
OIDC_ISSUER_URL=http://tflive.localhost:8082/realms/tflive
KEYCLOAK_ADMIN_URL=http://keycloak:8082
```

Keycloak now advertises the `tflive.localhost` issuer however it is reached, and
the API compares the discovery document's issuer to `OIDC_ISSUER_URL` exactly. A
stale `localhost:8082` value fails that check at startup rather than at sign-in,
which is easy to misread as Keycloak being down.

## Developing against the stack

Run the API, worker, and UI on the host against the containerised dependencies.
`.env` overrides the defaults compose supplies inline.

```bash
cp .env.example .env
docker compose up -d postgres keycloak keycloak-provision openfga-migrate openfga openfga-provision temporal
```

The identifiers the provisioner writes live inside a Docker volume, which a host
process cannot read, so copy them into `.env` once:

```bash
docker compose run --rm openfga-provision bootstrap
# Copy the printed OPENFGA_STORE_ID and OPENFGA_MODEL_ID into .env.
```

Bootstrap reuses a matching store and model rather than creating duplicates, so
re-running it is safe. Run only one at a time: OpenFGA store names are not
unique, and bootstrap fails closed if the `tflive` name is ambiguous.

Then, in separate shells:

```bash
set -a
source .env
set +a
go run ./cmd/api
```

```bash
set -a
source .env
set +a
go run ./cmd/worker
```

```bash
cd web
npm install
npm run dev
```

The Vite dev server proxies `/v1/*` and `/healthz` to the Go API. It binds port
`5173`, the same port as the `web` container, so run one or the other rather
than both.

Keycloak is reached at `http://tflive.localhost:8082`. That hostname resolves to
loopback in the browser and to the container through Docker's DNS, which is what
lets one issuer string satisfy both. Note that `curl` resolves `*.localhost` to
its own loopback and ignores the Docker alias, so a `curl` test from inside a
container will fail misleadingly — use `wget` or a language HTTP client.

The OpenFGA provisioner writes the store and model identifiers to a shared
volume, and the API and worker read them from `OPENFGA_STORE_ID_FILE` and
`OPENFGA_MODEL_ID_FILE`. Setting `OPENFGA_STORE_ID` or `OPENFGA_MODEL_ID`
directly still takes precedence. See
[Authentication and authorization](docs/authentication.md) for the role matrix
and the immutable-model update procedure.

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
