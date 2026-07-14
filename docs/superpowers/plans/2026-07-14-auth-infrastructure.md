# Local Authentication Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add pinned, persistent, health-checked Keycloak and OpenFGA services to the existing local Docker Compose stack without regressing tflive, Temporal, Postgres, or MinIO startup.

**Architecture:** Keycloak 26.6.3 and OpenFGA v1.15.1 each use a dedicated Postgres 16 datastore and a named development volume. Keycloak runs in development mode with health checks enabled; OpenFGA runs its database migration as a one-shot dependency before the server starts. All local credentials are required Compose substitutions supplied by `.env.example` or a developer-owned `.env` file.

**Tech Stack:** Docker Compose, PostgreSQL 16, Keycloak 26.6.3, OpenFGA v1.15.1, Node.js standard library contract test.

## Global Constraints

- Preserve all existing service names, ports, volumes, and startup behavior.
- Pin Keycloak to `quay.io/keycloak/keycloak:26.6.3` and OpenFGA to `openfga/openfga:v1.15.1`.
- Use `postgres:16-alpine` for both new datastores, consistent with the existing local stack.
- Store Keycloak and OpenFGA data in separate named volumes.
- Require every new database and bootstrap credential from the Compose environment; do not add Compose fallback passwords.
- Keep all example credentials explicitly local-development-only.
- Do not provision a Keycloak realm, OpenFGA store, or authorization model in this ticket; those belong to AUTH-003 and AUTH-004.

---

## File Structure

- Create `scripts/verify-auth-compose.mjs`: dependency-free contract test for pinned images, health checks, dependency ordering, required environment substitutions, and persistent volumes.
- Modify `docker-compose.yaml`: add the two databases, Keycloak, OpenFGA migration, OpenFGA server, ports, health checks, and named volumes.
- Modify `.env.example`: document local-only Keycloak and OpenFGA database/bootstrap credentials.
- Modify `README.md`: document full-stack startup and local service endpoints.
- Modify `docs/sprint/authn_and_authz/README.md`: track AUTH-002 from `In Progress` to `Done` only after live verification.

### Task 1: Add the Authentication Compose Contract Test

**Files:**
- Create: `scripts/verify-auth-compose.mjs`

**Interfaces:**
- Consumes: `docker compose --env-file .env.example config --format json` and the raw `docker-compose.yaml` source.
- Produces: an exit-zero contract check used locally and in future CI to prevent floating auth images, missing health checks, unguarded credentials, or ephemeral datastores.

- [ ] **Step 1: Write the failing contract test**

Create `scripts/verify-auth-compose.mjs`:

```javascript
#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const rendered = execFileSync(
  "docker",
  ["compose", "--env-file", ".env.example", "config", "--format", "json"],
  { cwd: root, encoding: "utf8" },
);
const config = JSON.parse(rendered);
const source = readFileSync(resolve(root, "docker-compose.yaml"), "utf8");

function service(name) {
  const value = config.services?.[name];
  assert.ok(value, `missing Compose service: ${name}`);
  return value;
}

function hasVolume(value, sourceName) {
  return value.volumes?.some(
    (volume) => volume.type === "volume" && volume.source === sourceName,
  );
}

const keycloakPostgres = service("keycloak-postgres");
const keycloak = service("keycloak");
const openfgaPostgres = service("openfga-postgres");
const openfgaMigrate = service("openfga-migrate");
const openfga = service("openfga");

assert.equal(keycloakPostgres.image, "postgres:16-alpine");
assert.equal(keycloak.image, "quay.io/keycloak/keycloak:26.6.3");
assert.equal(openfgaPostgres.image, "postgres:16-alpine");
assert.equal(openfgaMigrate.image, "openfga/openfga:v1.15.1");
assert.equal(openfga.image, "openfga/openfga:v1.15.1");

assert.ok(keycloakPostgres.healthcheck, "Keycloak Postgres needs a health check");
assert.ok(keycloak.healthcheck?.test?.join(" ").includes("/health/ready"));
assert.equal(keycloak.depends_on?.["keycloak-postgres"]?.condition, "service_healthy");
assert.ok(openfgaPostgres.healthcheck, "OpenFGA Postgres needs a health check");
assert.equal(openfgaMigrate.depends_on?.["openfga-postgres"]?.condition, "service_healthy");
assert.equal(openfga.depends_on?.["openfga-migrate"]?.condition, "service_completed_successfully");
assert.ok(openfga.healthcheck?.test?.join(" ").includes("grpc_health_probe"));

assert.ok(hasVolume(keycloakPostgres, "keycloak-postgres-data"));
assert.ok(hasVolume(openfgaPostgres, "openfga-postgres-data"));
assert.ok(config.volumes?.["keycloak-postgres-data"]);
assert.ok(config.volumes?.["openfga-postgres-data"]);

for (const name of [
  "KEYCLOAK_DB_NAME",
  "KEYCLOAK_DB_USER",
  "KEYCLOAK_DB_PASSWORD",
  "KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME",
  "KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD",
  "OPENFGA_DB_NAME",
  "OPENFGA_DB_USER",
  "OPENFGA_DB_PASSWORD",
]) {
  assert.match(source, new RegExp(`\\$\\{${name}:\\?`), `${name} must be required`);
}

console.log("authentication Compose contract verified");
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `node scripts/verify-auth-compose.mjs`

Expected: FAIL with `missing Compose service: keycloak-postgres`.

- [ ] **Step 3: Commit the red contract test with the implementation in Task 2**

Do not commit a deliberately failing main branch. Keep the test uncommitted until Task 2 makes it pass.

### Task 2: Add Keycloak and OpenFGA to Docker Compose

**Files:**
- Modify: `docker-compose.yaml`
- Test: `scripts/verify-auth-compose.mjs`

**Interfaces:**
- Consumes: required variables documented in Task 3.
- Produces: Keycloak HTTP on host port `8082`, OpenFGA HTTP on `8083`, OpenFGA gRPC on `8084`, and internal management/health endpoints.

- [ ] **Step 1: Add the Keycloak datastore and server**

Add these services after `app-postgres`:

```yaml
  keycloak-postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${KEYCLOAK_DB_USER:?set KEYCLOAK_DB_USER in .env}
      POSTGRES_PASSWORD: ${KEYCLOAK_DB_PASSWORD:?set KEYCLOAK_DB_PASSWORD in .env}
      POSTGRES_DB: ${KEYCLOAK_DB_NAME:?set KEYCLOAK_DB_NAME in .env}
    volumes:
      - keycloak-postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${KEYCLOAK_DB_USER:?set KEYCLOAK_DB_USER in .env} -d ${KEYCLOAK_DB_NAME:?set KEYCLOAK_DB_NAME in .env}"]
      interval: 5s
      timeout: 5s
      retries: 10

  keycloak:
    image: quay.io/keycloak/keycloak:26.6.3
    command: start-dev
    depends_on:
      keycloak-postgres:
        condition: service_healthy
    environment:
      KC_DB: postgres
      KC_DB_URL: jdbc:postgresql://keycloak-postgres:5432/${KEYCLOAK_DB_NAME:?set KEYCLOAK_DB_NAME in .env}
      KC_DB_USERNAME: ${KEYCLOAK_DB_USER:?set KEYCLOAK_DB_USER in .env}
      KC_DB_PASSWORD: ${KEYCLOAK_DB_PASSWORD:?set KEYCLOAK_DB_PASSWORD in .env}
      KC_BOOTSTRAP_ADMIN_USERNAME: ${KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME:?set KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME in .env}
      KC_BOOTSTRAP_ADMIN_PASSWORD: ${KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD:?set KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD in .env}
      KC_HEALTH_ENABLED: "true"
      KC_METRICS_ENABLED: "true"
    ports:
      - "8082:8080"
    healthcheck:
      test:
        - CMD-SHELL
        - >-
          { printf 'HEAD /health/ready HTTP/1.0\r\n\r\n' >&0;
          grep 'HTTP/1.0 200'; } 0<>/dev/tcp/localhost/9000
      interval: 10s
      timeout: 5s
      retries: 18
      start_period: 30s
```

- [ ] **Step 2: Add the OpenFGA datastore, migration, and server**

Add these services after Keycloak:

```yaml
  openfga-postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${OPENFGA_DB_USER:?set OPENFGA_DB_USER in .env}
      POSTGRES_PASSWORD: ${OPENFGA_DB_PASSWORD:?set OPENFGA_DB_PASSWORD in .env}
      POSTGRES_DB: ${OPENFGA_DB_NAME:?set OPENFGA_DB_NAME in .env}
    volumes:
      - openfga-postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${OPENFGA_DB_USER:?set OPENFGA_DB_USER in .env} -d ${OPENFGA_DB_NAME:?set OPENFGA_DB_NAME in .env}"]
      interval: 5s
      timeout: 5s
      retries: 10

  openfga-migrate:
    image: openfga/openfga:v1.15.1
    command: migrate
    depends_on:
      openfga-postgres:
        condition: service_healthy
    environment:
      OPENFGA_DATASTORE_ENGINE: postgres
      OPENFGA_DATASTORE_URI: postgres://${OPENFGA_DB_USER:?set OPENFGA_DB_USER in .env}:${OPENFGA_DB_PASSWORD:?set OPENFGA_DB_PASSWORD in .env}@openfga-postgres:5432/${OPENFGA_DB_NAME:?set OPENFGA_DB_NAME in .env}?sslmode=disable
    restart: "no"

  openfga:
    image: openfga/openfga:v1.15.1
    command: run
    depends_on:
      openfga-migrate:
        condition: service_completed_successfully
    environment:
      OPENFGA_DATASTORE_ENGINE: postgres
      OPENFGA_DATASTORE_URI: postgres://${OPENFGA_DB_USER:?set OPENFGA_DB_USER in .env}:${OPENFGA_DB_PASSWORD:?set OPENFGA_DB_PASSWORD in .env}@openfga-postgres:5432/${OPENFGA_DB_NAME:?set OPENFGA_DB_NAME in .env}?sslmode=disable
      OPENFGA_LOG_FORMAT: json
      OPENFGA_PLAYGROUND_ENABLED: "false"
    ports:
      - "8083:8080"
      - "8084:8081"
    healthcheck:
      test: ["CMD", "/usr/local/bin/grpc_health_probe", "-addr=localhost:8081"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 10s
```

Add the volumes:

```yaml
volumes:
  app-postgres-data:
  keycloak-postgres-data:
  openfga-postgres-data:
  temporal-postgres-data:
  minio-data:
```

- [ ] **Step 3: Run the contract test to verify the Compose implementation passes**

Run: `node scripts/verify-auth-compose.mjs`

Expected: PASS with `authentication Compose contract verified`.

- [ ] **Step 4: Validate Compose normalization**

Run: `docker compose --env-file .env.example config --quiet`

Expected: exit 0 with no output.

- [ ] **Step 5: Commit the Compose contract and implementation**

```bash
git add scripts/verify-auth-compose.mjs docker-compose.yaml
git commit -m "infra: add local auth services"
```

### Task 3: Document Local Credentials and Startup

**Files:**
- Modify: `.env.example`
- Modify: `README.md`
- Modify: `docs/sprint/authn_and_authz/README.md`

**Interfaces:**
- Consumes: Compose variable names from Task 2.
- Produces: a clean-checkout startup path with explicitly local-only credentials and discoverable service endpoints.

- [ ] **Step 1: Add local-only environment values**

Add an `Authentication and authorization services` section to `.env.example`:

```dotenv
# Local development only. Production must provide every value below through a
# deployment secret manager and must not reuse these example credentials.
KEYCLOAK_DB_NAME=keycloak
KEYCLOAK_DB_USER=keycloak
KEYCLOAK_DB_PASSWORD=keycloak-local-only
KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME=tflive-admin
KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD=tflive-admin-local-only

OPENFGA_DB_NAME=openfga
OPENFGA_DB_USER=openfga
OPENFGA_DB_PASSWORD=openfga-local-only
```

- [ ] **Step 2: Update local startup documentation**

Change the README startup command to:

```bash
cp .env.example .env
docker compose up app-postgres keycloak-postgres keycloak openfga-postgres openfga-migrate openfga temporal-postgres temporal temporal-ui minio minio-init
```

Document these endpoints:

```text
Keycloak:     http://localhost:8082
OpenFGA HTTP: http://localhost:8083
OpenFGA gRPC: localhost:8084
```

State that the example credentials are local-only and that AUTH-003 provisions
the tflive realm after the infrastructure is healthy.

- [ ] **Step 3: Mark AUTH-002 In Progress while live verification runs**

Change only the AUTH-002 backlog status from `Not Started` to `In Progress`.

- [ ] **Step 4: Re-run the static contract checks**

Run:

```bash
node scripts/verify-auth-compose.mjs
docker compose --env-file .env.example config --quiet
```

Expected: both commands pass.

- [ ] **Step 5: Commit documentation and tracking**

```bash
git add .env.example README.md docs/sprint/authn_and_authz/README.md
git commit -m "docs: document local auth services"
```

### Task 4: Verify the Live Stack and Complete AUTH-002

**Files:**
- Modify: `docs/sprint/authn_and_authz/README.md`

**Interfaces:**
- Consumes: the complete Compose stack and `.env.example`.
- Produces: verified healthy services, persistent volumes, green repository tests, and a completed GitHub issue.

- [ ] **Step 1: Pull the pinned images**

Run:

```bash
docker compose --env-file .env.example pull keycloak-postgres keycloak openfga-postgres openfga-migrate openfga
```

Expected: all five images resolve without a floating tag.

- [ ] **Step 2: Start the complete local dependency stack**

Run:

```bash
docker compose --env-file .env.example up -d app-postgres keycloak-postgres keycloak openfga-postgres openfga-migrate openfga temporal-postgres temporal temporal-ui minio minio-init
```

Expected: database services become healthy, `openfga-migrate` exits 0, and Keycloak/OpenFGA become healthy without stopping existing services.

- [ ] **Step 3: Verify service and persistence state**

Run:

```bash
docker compose --env-file .env.example ps
curl --fail http://localhost:8083/healthz
docker volume inspect tflive-compose_keycloak-postgres-data tflive-compose_openfga-postgres-data
```

Expected: OpenFGA returns `{"status":"SERVING"}`, Keycloak and OpenFGA show healthy, and both named volumes exist.

- [ ] **Step 4: Run repository verification**

Run:

```bash
node scripts/verify-auth-compose.mjs
docker compose --env-file .env.example config --quiet
go test ./...
npm --prefix web test
npm --prefix web run build
```

Expected: every command exits 0.

- [ ] **Step 5: Mark the backlog ticket Done and commit**

Change AUTH-002 from `In Progress` to `Done`, then run:

```bash
git add docs/sprint/authn_and_authz/README.md
git commit -m "docs: complete auth infrastructure ticket"
```

- [ ] **Step 6: Push and close GitHub issue #4**

Push the commits to `origin/main`. Update the issue checklist, comment with the
image versions, service endpoints, commit links, and verification evidence, then
close AUTH-002.

## References

- Keycloak 26.6.3 release: <https://github.com/keycloak/keycloak/releases/tag/26.6.3>
- Keycloak container health checks: <https://www.keycloak.org/observability/health>
- OpenFGA v1.15.1 release: <https://github.com/openfga/openfga/releases/tag/v1.15.1>
- OpenFGA Docker/Postgres setup: <https://openfga.dev/docs/getting-started/setup-openfga/docker>
- OpenFGA health checks: <https://openfga.dev/docs/getting-started/setup-openfga/configure-openfga#health-check>
