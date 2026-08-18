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

Compose builds and runs the full product across two files. `docker-compose.yaml`
holds one Postgres server hosting every component database, Keycloak, OpenFGA,
Temporal, and the provisioners; `docker-compose.app.yaml` overlays the `api`,
`worker`, and `web` services built from source in this repository. The split
lets the authorization store and model be provisioned and recorded before
anything runs against them; the application phase names both files so they merge
into one project and the startup ordering between them survives. Provisioning runs as
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
| volumes | 5 | 4 (3 active by default) |
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
- `docker-compose.app.yaml` declares the two OpenFGA identifiers required, so
  the application phase cannot start against an unrecorded store or model.

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

`openfga-provision bootstrap` prints the store and model identifiers to stdout
during the infrastructure phase. The operator copies both into `.env` before
starting the application phase, which declares them required and refuses to
interpolate without them, naming the missing variable.

A file-based handoff over a shared volume was built first and then removed. It
made the two phases collapse into one command, which is precisely what the split
exists to prevent: the store and model an environment runs against should be a
deliberate, recorded choice, not whatever a bootstrap produced. Removing it also
deleted the `_FILE` fallbacks from `internal/config` and `internal/openfga`, the
shared volume, and the directory-ownership handling its non-root writer needed.

The exactness property is unchanged and now simpler to state: identifiers only
ever arrive as explicit configuration. Nothing discovers a store by name at
runtime and nothing resolves the latest model.

The API does not check the pair at startup — it starts, and authorization fails
closed at request time. `openfga-provision verify` is the up-front check, reading
the exact pair from the environment and comparing it against the live store and
model without mutating either.

## Issuer Resolution

`internal/authn` derives the discovery URL from the configured issuer, requires
the discovery document's issuer to equal it exactly, and takes the JWKS URL from
that document. One issuer string must therefore be resolvable from the browser
and from inside the `api` container, and must match what Keycloak advertises.
`localhost:8082` satisfies the browser and Keycloak but not the container.

Keycloak is configured with `KC_HOSTNAME=http://keycloak.localhost:8082`,
`KC_HTTP_PORT=8082`, a published mapping of `8082:8082`, and a Docker network
alias of `keycloak.localhost`. The browser resolves `keycloak.localhost` to loopback
and reaches the published port; `api` resolves the same name through Docker DNS
and reaches the container directly. Matching the internal and external port is
required because the port is part of the issuer string.

`VITE_OIDC_ISSUER` and `OIDC_ISSUER_URL` are both set to
`http://keycloak.localhost:8082/realms/tflive`. `KEYCLOAK_WEB_REDIRECT_URIS` and
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

Verified: `keycloak.localhost` resolves to `127.0.0.1` on macOS (`dscacheutil`,
`ping`), and Keycloak 26.6.3 serves a discovery document with `issuer` and
`jwks_uri` both on the `http://keycloak.localhost:8082` origin when
`KC_HTTP_PORT` and the published port are both 8082. From a sibling container
on a Docker network with `--alias keycloak.localhost` on the Keycloak container,
a Go 1.24.1 binary using `net/http`, built `CGO_ENABLED=0` — the build the api
image will use — resolved the alias and received the identical discovery
document, confirming the API's actual resolution path works; a separate
`nslookup` check against Docker's embedded DNS (`127.0.0.11`) during the
earlier diagnostic run confirmed that server answers the alias correctly,
which is the resolver Go's `net/http` consults by default in a container.
Resolution is client-dependent, though: `curl`, on both musl and glibc,
hardcodes `*.localhost` to loopback per its own RFC 6761 handling and never
consults the system or Docker resolver, so it fails inside a sibling container
even though `getent`, `nslookup` against `127.0.0.11`, `wget`, and Go's
`net/http` all resolve the alias correctly. Anyone debugging this stack with
`curl` from inside a container will see a spurious connection-refused on
`keycloak.localhost` and should use `wget` or a language HTTP client instead. No
`/etc/hosts` fallback is needed.

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
