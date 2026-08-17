# One-Command Local Stack Design

## Goal

Reduce first-run setup from roughly ten manual steps across two toolchains to
`docker compose up`, without weakening any authentication or authorization
property and without removing Keycloak or OpenFGA from the stack.

## Current State

A clean checkout requires copying `.env.example` to `.env`, starting a partial
dependency set, running `openfga-provision bootstrap`, copying two identifiers
from stdout into `.env` by hand, running `openfga-provision verify`, starting
the remaining dependencies, then running `tflive-api`, `tflive-worker`, and the
Vite dev server as three separate host processes with a Go toolchain and a Node
toolchain installed. `docker-compose.yaml` defines thirteen services and five
volumes, four of which are Postgres instances. `.env.example` defines
forty-seven variables.

Two properties of the existing code make the automation possible. `Bootstrap`
reuses a store whose name already matches and reuses a semantically equal
authorization model, creating either only when absent, so it converges when run
repeatedly. Schema migrations are embedded and applied during API and worker
startup, so no separate migration step is required.

## Architecture

Compose builds and runs the full product: one Postgres server hosting every
component database, Keycloak, OpenFGA, Temporal, and the `api`, `worker`, and
`web` services built from source in this repository. Provisioning runs as
one-shot services that exit zero before dependents start. OpenFGA identifiers
move from a human copy-paste step to a file on a shared volume. Artifact storage
defaults to the filesystem backend on a volume shared by `api` and `worker`,
which removes MinIO from the default path.

The host-based development loop is unchanged. `go run ./cmd/api`,
`go run ./cmd/worker`, and `npm run dev` continue to read `.env` and continue to
work against the same dependency services.

## Service Topology

| | current | designed |
| --- | --- | --- |
| services | 13 | 10 |
| long-running services | 9 | 7 |
| volumes | 5 | 2 |
| host processes | 3 | 0 |
| required toolchains | Go, Node | none |

The seven long-running services are `postgres`, `keycloak`, `openfga`,
`temporal`, `api`, `worker`, and `web`. The three one-shots are
`keycloak-provision`, `openfga-migrate`, and `openfga-provision`. Database
creation runs from the Postgres image entrypoint rather than as a service.

## Components

- `Dockerfile.api` and `Dockerfile.worker` build static Go binaries in a builder
  stage and copy them onto a minimal runtime base. `Dockerfile.worker`
  additionally installs the OpenTofu binary the runner invokes.
- `Dockerfile.web` runs the Vite production build and serves `dist/` from nginx,
  proxying `/v1` and `/healthz` to `api` so the browser sees a single origin.
  This mirrors the existing Vite dev-server proxy.
- `deploy/postgres/init.sql` runs from `/docker-entrypoint-initdb.d` and creates
  the `keycloak`, `openfga`, `temporal`, and `temporal_visibility` databases and
  their owning roles. The application database is created by the image from
  `POSTGRES_DB`.
- `docker-compose.yaml` replaces `app-postgres`, `keycloak-postgres`,
  `openfga-postgres`, and `temporal-postgres` with a single `postgres` service,
  repoints `KC_DB_URL`, `OPENFGA_DATASTORE_URI`, and Temporal's `POSTGRES_SEEDS`
  at it, and adds the three application services.
- `cmd/openfga-provisioner` gains an identifier-file output path.
- `internal/config` and `internal/openfga/config.go` gain file-backed fallbacks
  for the two OpenFGA identifiers.

## Database Consolidation

One `postgres:16-alpine` service hosts five databases. Every consumer already
connects over TCP with a host, port, database, user, and password, so
consolidation is a connection-string change plus role and database creation.

Temporal requires both `temporal` and `temporal_visibility` to exist and to be
owned by its role. `temporalio/auto-setup` creates databases itself when absent;
pre-creating them in the init script and granting ownership keeps auto-setup's
schema step working without granting it `CREATEDB`.

The init script runs only on first initialization of an empty data directory.
Existing local volumes therefore need `docker compose down -v` once. This is a
one-time cost for current developers and invisible to fresh clones.

## OpenFGA Identifier Handoff

`openfga-provision bootstrap` writes the store and model identifiers to files on
a named volume mounted read-write by the provisioner and read-only by `api` and
`worker`. Two files are written, one identifier per file, matching the Docker
secrets convention.

`api` and `worker` accept `OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE`.
Resolution order for each identifier is: the direct environment variable when
non-empty; otherwise the contents of the corresponding file with surrounding
whitespace trimmed; otherwise the existing required-variable error. A file that
is configured but unreadable or empty is an error rather than a fallthrough, so
a broken handoff fails at startup instead of surfacing as an authorization
failure later. Values read from a file are validated identically to values read
from the environment, including the existing rejection of whitespace and control
characters.

This preserves the property the current manual step protects. The API still
consumes exact, pinned identifiers and still never resolves the latest model.
Only the transcription is automated.

An entrypoint wrapper that sources an environment file before executing the
binary was considered and rejected: it requires no Go changes but confines the
mechanism to containers, whereas the `_FILE` convention is equally usable under
Docker secrets, Kubernetes, and systemd credentials.

## Issuer Resolution

`internal/authn` derives the discovery URL from the configured issuer, requires
the discovery document's issuer to equal it exactly, and takes the JWKS URL from
that document. One issuer string must therefore be resolvable from the browser
and from inside the `api` container, and must match what Keycloak advertises.
`localhost:8082` satisfies the browser and Keycloak but not the container.

Keycloak is configured with `KC_HOSTNAME=http://tflive.localhost:8082`,
`KC_HTTP_PORT=8082`, a published mapping of `8082:8082`, and a Docker network
alias of `tflive.localhost`. The browser resolves `tflive.localhost` to loopback
and reaches the published port; `api` resolves the same name through Docker DNS
and reaches the container directly. Matching the internal and external port is
required because the port is part of the issuer string.

`VITE_OIDC_ISSUER` and `OIDC_ISSUER_URL` are both set to
`http://tflive.localhost:8082/realms/tflive`. `KEYCLOAK_WEB_REDIRECT_URIS` and
`KEYCLOAK_WEB_ORIGINS` continue to name the browser origin of the `web`
service.

No change is made to the issuer equality check. Splitting the discovery fetch
URL from the issuer identity is tracked separately in #199 and is not required
here.

## Configuration

Compose supplies defaults inline for every variable the stack needs, so
`cp .env.example .env` stops being a prerequisite. A present `.env` continues to
override those defaults, preserving the host-based workflow. `.env.example`
remains the reference for host-based development and for production-shaped
configuration.

`ARTIFACT_STORE_KIND` defaults to `filesystem` in the compose environment.
`ARTIFACT_STORE_FILESYSTEM_ROOT` points at a named volume mounted by both `api`
and `worker`, because `api` reads the run logs `worker` writes.

## Startup Ordering

`postgres` becomes healthy, then `keycloak` becomes healthy and
`openfga-migrate` completes; `keycloak-provision` and `openfga-provision`
complete; `api` and `worker` start. `web` depends on `api`. Every one-shot is
gated with `service_completed_successfully` and every long-running dependency
with `service_healthy`. `api` and `worker` apply schema migrations themselves
during startup.

## Profiles

MinIO and `minio-init` move behind a `s3` profile so the S3 artifact adapter
stays exercisable locally. `temporal-ui` moves behind a `debug` profile. Neither
starts by default, and both retain their current configuration.

## Failure Semantics

- A provisioner that fails exits non-zero and blocks its dependents rather than
  starting the application against unprovisioned infrastructure.
- Re-running the stack re-runs both provisioners. Both converge rather than
  duplicating: `Bootstrap` reuses a matching store and model, and the Keycloak
  provisioner reconciles its realm.
- An unreadable or empty identifier file fails API and worker startup.
- A stale identifier file naming a store that no longer exists fails the
  existing verification path rather than silently authorizing.

## Risks

Resolution of `*.localhost` to loopback is specified by RFC 6761 and implemented
by Chrome, Firefox, and macOS, but has historical variance across resolvers.
This is the single assumption the identity path depends on, so it is verified
first, before any other work in the implementation, with a documented
`/etc/hosts` fallback if it does not hold.

Keycloak's behavior when its internal and external ports match is assumed rather
than verified; this is confirmed against 26.6.3 during the same first step.

First `docker compose up` compiles Go binaries and runs a Vite production build,
so it is materially slower than subsequent runs. Published images would remove
this but require a release pipeline, which this repository does not have.

## Scope

This change covers local packaging and provisioning only. It does not change
application behavior, authentication, or the authorization model. It does not
introduce continuous integration or publish images. It does not reduce the
number of dependencies the product requires, only the number of steps and
processes needed to run them. Extracting Keycloak provisioning (#197) overlaps
with this work and is deliberately sequenced after it, because the demo path
depends on provisioning behaving as it does today.

## Verification

- `docker compose up` from a clean checkout with no `.env` present reaches a
  working UI, including login, on a machine with only Docker installed.
- `docker compose up` a second time, and after `docker compose down`, succeeds
  without manual intervention.
- A template run executes end to end through the containerized worker, and its
  logs are readable through the API from the shared artifact volume.
- `docker compose --profile s3 up` exercises the S3 adapter against MinIO.
- Config tests prove the resolution order for both identifiers, including
  direct-value precedence, file fallback, whitespace trimming, and the error
  paths for missing, empty, and unreadable files.
- The host-based workflow continues to work from `.env` unchanged.
- `go test ./...` and `npm test` remain green.
