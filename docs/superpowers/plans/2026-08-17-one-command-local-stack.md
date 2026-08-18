# One-Command Local Stack Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce first-run setup from roughly ten manual steps across two toolchains to `docker compose up`, with no change to authentication or authorization behavior.

**Architecture:** Consolidate four Postgres instances into one server hosting five databases. Replace the manual OpenFGA identifier copy-paste with a file handoff on a shared volume, read through new `*_FILE` config fallbacks. Containerize `api`, `worker`, and `web`, built from source by compose. Move MinIO and `temporal-ui` behind opt-in profiles.

**Tech Stack:** Go 1.24, Docker Compose, Postgres 16, Keycloak 26.6.3, OpenFGA v1.15.1, Temporal 1.28.1, OpenTofu, Vite/React, nginx.

**Spec:** `docs/superpowers/specs/2026-08-17-one-command-local-stack-design.md`

## Global Constraints

- Go floor is `go 1.24.0` with `toolchain go1.24.1` (`go.mod`). Do not raise either.
- Pinned images, unchanged from the current compose file: `postgres:16-alpine`, `quay.io/keycloak/keycloak:26.6.3`, `openfga/openfga:v1.15.1`, `temporalio/auto-setup:1.28.1`, `temporalio/ui:2.49.1`.
- The canonical issuer string is `http://keycloak.localhost:8082/realms/tflive`. It must be byte-identical everywhere it appears: `OIDC_ISSUER_URL`, `VITE_OIDC_ISSUER`, and Keycloak's `KC_HOSTNAME` origin.
- Keycloak's internal and external ports must both be `8082`, because the port is part of the issuer string.
- No change to `internal/authn`. The issuer equality check at `internal/authn/oidc_provider.go:65` stays exactly as it is.
- OpenFGA store and model identifiers are **not secrets** — they are already printed to stdout by the current documented workflow. Identifier files are mode `0644`.
- Existing behavior must not regress: a present `.env` still overrides compose defaults, and the host-based `go run` + `npm run dev` workflow keeps working.
- Every Go change follows TDD: failing test first, then minimal implementation.

---

### Task 1: Verify the issuer-resolution assumptions

The whole identity path depends on two unverified assumptions. The spec requires
confirming both before any other work. This task writes no production code; its
deliverable is a go/no-go recorded in the spec.

**Files:**
- Modify: `docs/superpowers/specs/2026-08-17-one-command-local-stack-design.md` (Risks section)

**Interfaces:**
- Consumes: nothing.
- Produces: a confirmed or rejected `keycloak.localhost` alias scheme. Every later task assumes it holds.

- [ ] **Step 1: Confirm `*.localhost` resolves to loopback on this machine**

```bash
dscacheutil -q host -a name keycloak.localhost
ping -c 1 keycloak.localhost
```

Expected: resolves to `127.0.0.1`. If it does not resolve, the fallback is a
`/etc/hosts` line (`127.0.0.1 keycloak.localhost`), which must then be documented
as a prerequisite in Task 8 — that materially weakens "one command", so record
it explicitly.

- [ ] **Step 2: Confirm Keycloak serves correctly when internal and external ports match**

```bash
docker run --rm -d --name kc-probe -p 8082:8082 \
  -e KC_BOOTSTRAP_ADMIN_USERNAME=admin \
  -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
  -e KC_HTTP_PORT=8082 \
  -e KC_HOSTNAME=http://keycloak.localhost:8082 \
  quay.io/keycloak/keycloak:26.6.3 start-dev
sleep 25
curl -s http://keycloak.localhost:8082/realms/master/.well-known/openid-configuration | head -c 400
```

Expected: a discovery document whose `issuer` is exactly
`http://keycloak.localhost:8082/realms/master`, and whose `jwks_uri` is on the
same origin.

- [ ] **Step 3: Confirm the same name resolves from inside a sibling container**

```bash
docker network create kc-probe-net
docker network connect --alias keycloak.localhost kc-probe-net kc-probe
docker run --rm --network kc-probe-net alpine:3.21 \
  sh -c "apk add --no-cache curl >/dev/null && curl -s http://keycloak.localhost:8082/realms/master/.well-known/openid-configuration | head -c 200"
```

Expected: the same discovery document, fetched by container name resolution.
This is the assumption the API depends on.

- [ ] **Step 4: Tear down the probe**

```bash
docker rm -f kc-probe
docker network rm kc-probe-net
```

- [ ] **Step 5: Record the outcome in the spec and commit**

Replace the first paragraph of the spec's `## Risks` section with the verified
result, naming the Keycloak version and the resolver behavior observed. If any
step failed, STOP and report before continuing — the alias scheme needs
redesigning and the remaining tasks rest on it.

```bash
git add docs/superpowers/specs/2026-08-17-one-command-local-stack-design.md
git commit -m "docs: verify keycloak.localhost issuer resolution assumptions"
```

---

### Task 2: File-backed OpenFGA identifier resolution

`api` and `worker` both read the OpenFGA identifiers through
`loadOpenFGAConfig`, so this single function is the only consumer-side change.

**Files:**
- Modify: `internal/config/auth.go:194-224` (`loadOpenFGAConfig`)
- Test: `internal/config/auth_test.go`

**Interfaces:**
- Consumes: existing `authConfigError(format string, args ...any) error` and `safeOpaqueValue(string) bool`, both already in `internal/config/auth.go`.
- Produces: `resolveOpenFGAIdentifier(getenv func(string) string, name string) (string, error)` in package `config`. Reads `<name>`, falling back to the file at `<name>_FILE`. Task 7 relies on the env var names `OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/auth_test.go`:

```go
func TestLoadOpenFGAIdentifiersFromFiles(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store_id")
	modelPath := filepath.Join(dir, "model_id")
	if err := os.WriteFile(storePath, []byte("store-from-file\n"), 0o644); err != nil {
		t.Fatalf("write store file: %v", err)
	}
	if err := os.WriteFile(modelPath, []byte("  model-from-file  "), 0o644); err != nil {
		t.Fatalf("write model file: %v", err)
	}

	values := validSecurityValues()
	values["OPENFGA_STORE_ID"] = ""
	values["OPENFGA_MODEL_ID"] = ""
	values["OPENFGA_STORE_ID_FILE"] = storePath
	values["OPENFGA_MODEL_ID_FILE"] = modelPath

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig() error = %v", err)
	}
	if cfg.OpenFGA.StoreID != "store-from-file" {
		t.Fatalf("StoreID = %q, want %q", cfg.OpenFGA.StoreID, "store-from-file")
	}
	if cfg.OpenFGA.ModelID != "model-from-file" {
		t.Fatalf("ModelID = %q, want %q", cfg.OpenFGA.ModelID, "model-from-file")
	}
}

func TestLoadOpenFGADirectValueWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store_id")
	if err := os.WriteFile(storePath, []byte("store-from-file"), 0o644); err != nil {
		t.Fatalf("write store file: %v", err)
	}

	values := validSecurityValues()
	values["OPENFGA_STORE_ID"] = "store-from-env"
	values["OPENFGA_STORE_ID_FILE"] = storePath

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig() error = %v", err)
	}
	if cfg.OpenFGA.StoreID != "store-from-env" {
		t.Fatalf("StoreID = %q, want the direct value to win", cfg.OpenFGA.StoreID)
	}
}

func TestLoadOpenFGAIdentifierFileErrors(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyPath, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}
	unsafePath := filepath.Join(dir, "unsafe")
	if err := os.WriteFile(unsafePath, []byte("bad id"), 0o644); err != nil {
		t.Fatalf("write unsafe file: %v", err)
	}

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "missing file", file: filepath.Join(dir, "absent"), want: "OPENFGA_STORE_ID_FILE could not be read"},
		{name: "empty file", file: emptyPath, want: "OPENFGA_STORE_ID_FILE is empty"},
		{name: "unsafe contents", file: unsafePath, want: "OPENFGA_STORE_ID must not contain whitespace or control characters"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validSecurityValues()
			values["OPENFGA_STORE_ID"] = ""
			values["OPENFGA_STORE_ID_FILE"] = test.file

			_, err := loadSecurityConfig(mapConfigEnv(values))
			if !errors.Is(err, ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidConfig containing %q", err, test.want)
			}
		})
	}
}

func TestLoadOpenFGAIdentifierRequiredWhenNeitherSet(t *testing.T) {
	values := validSecurityValues()
	values["OPENFGA_STORE_ID"] = ""
	values["OPENFGA_STORE_ID_FILE"] = ""

	_, err := loadSecurityConfig(mapConfigEnv(values))
	if !errors.Is(err, ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "OPENFGA_STORE_ID is required") {
		t.Fatalf("error = %v, want ErrInvalidConfig containing %q", err, "OPENFGA_STORE_ID is required")
	}
}
```

Add `"os"` and `"path/filepath"` to the imports of `internal/config/auth_test.go` if they are not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run TestLoadOpenFGA -v`
Expected: FAIL — the file-backed cases report `OPENFGA_STORE_ID is required` because the fallback does not exist yet.

- [ ] **Step 3: Implement the resolver**

In `internal/config/auth.go`, add `"os"` to the imports and add:

```go
// resolveOpenFGAIdentifier reads an OpenFGA identifier from the environment,
// falling back to the file named by "<name>_FILE". The file form lets a
// provisioner hand identifiers to the API and worker through a shared volume
// without a human transcribing them. A configured but unusable file is an
// error rather than a fallthrough, so a broken handoff fails at startup
// instead of surfacing later as an authorization failure.
func resolveOpenFGAIdentifier(getenv func(string) string, name string) (string, error) {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		if !safeOpaqueValue(value) {
			return "", authConfigError("%s must not contain whitespace or control characters", name)
		}
		return value, nil
	}

	path := strings.TrimSpace(getenv(name + "_FILE"))
	if path == "" {
		return "", authConfigError("%s is required", name)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", authConfigError("%s_FILE could not be read", name)
	}
	value := strings.TrimSpace(string(contents))
	if value == "" {
		return "", authConfigError("%s_FILE is empty", name)
	}
	if !safeOpaqueValue(value) {
		return "", authConfigError("%s must not contain whitespace or control characters", name)
	}
	return value, nil
}
```

Then replace the identifier block inside `loadOpenFGAConfig` — the twelve lines
from `storeID := strings.TrimSpace(...)` through the `modelID` validation — with:

```go
	storeID, err := resolveOpenFGAIdentifier(getenv, "OPENFGA_STORE_ID")
	if err != nil {
		return OpenFGAConfig{}, err
	}
	modelID, err := resolveOpenFGAIdentifier(getenv, "OPENFGA_MODEL_ID")
	if err != nil {
		return OpenFGAConfig{}, err
	}
```

The existing `apiURL, err := parseConfigURL(...)` above already declares `err`,
so use `=` rather than `:=` if the compiler reports a redeclaration.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including every pre-existing test in the package. The direct-value
path must be unchanged.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/auth.go internal/config/auth_test.go
git add internal/config/auth.go internal/config/auth_test.go
git commit -m "feat(config): resolve OpenFGA identifiers from files"
```

---

### Task 3: Identifier file output from the provisioner

**Files:**
- Modify: `cmd/openfga-provisioner/main.go:40-70` (`run`)
- Test: `cmd/openfga-provisioner/main_test.go`

**Interfaces:**
- Consumes: the existing `run(ctx, args, getenv, modelJSON, execute, stdout, stderr) error` signature. Do not change it.
- Produces: when `OPENFGA_ID_OUTPUT_DIR` is set, files `store_id` and `model_id` in that directory, each containing one identifier and no trailing newline. Task 7 mounts that directory and points `OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE` at those two paths.

- [ ] **Step 1: Write the failing test**

Append to `cmd/openfga-provisioner/main_test.go` (create the file with
`package main` and the imports below if it does not exist):

```go
func TestRunWritesIdentifierFiles(t *testing.T) {
	dir := t.TempDir()
	getenv := func(name string) string {
		switch name {
		case "OPENFGA_API_URL":
			return "http://openfga:8080"
		case "OPENFGA_ID_OUTPUT_DIR":
			return dir
		default:
			return ""
		}
	}
	execute := func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{StoreID: "store-123", ModelID: "model-456"}, nil
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"bootstrap"}, getenv, openfgamodel.AuthorizationModelJSON(), execute, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(dir, "store_id"): "store-123",
		filepath.Join(dir, "model_id"): "model-456",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(contents) != want {
			t.Fatalf("%s = %q, want %q", path, string(contents), want)
		}
	}
}

func TestRunWithoutOutputDirWritesNoFiles(t *testing.T) {
	dir := t.TempDir()
	getenv := func(name string) string {
		if name == "OPENFGA_API_URL" {
			return "http://openfga:8080"
		}
		return ""
	}
	execute := func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{StoreID: "store-123", ModelID: "model-456"}, nil
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"bootstrap"}, getenv, openfgamodel.AuthorizationModelJSON(), execute, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("wrote %d files, want none", len(entries))
	}
	if !strings.Contains(stdout.String(), "OPENFGA_STORE_ID=store-123") {
		t.Fatalf("stdout = %q, want the existing assignments", stdout.String())
	}
}
```

Required imports: `bytes`, `context`, `os`, `path/filepath`, `strings`, `testing`,
plus `openfga "github.com/vishu42/tflive/internal/openfga"` and
`openfgamodel "github.com/vishu42/tflive/openfga"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/openfga-provisioner/ -run TestRunWrites -v`
Expected: FAIL — no `store_id` file exists, because nothing writes one.

- [ ] **Step 3: Implement the file output**

In `cmd/openfga-provisioner/main.go`, add `"path/filepath"` to the imports and
insert this immediately after the existing stdout `Fprintf` and before the
stderr diagnostic:

```go
	if err := writeIdentifierFiles(getenv("OPENFGA_ID_OUTPUT_DIR"), result); err != nil {
		return err
	}
```

Then add:

```go
// writeIdentifierFiles hands the resolved identifiers to the API and worker
// through a shared volume, replacing the manual copy into .env. The values are
// identifiers rather than secrets — the same values are printed to stdout by
// the documented workflow — so the files are world-readable.
func writeIdentifierFiles(dir string, result openfga.Result) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create OpenFGA identifier directory: %w", err)
	}
	for name, value := range map[string]string{
		"store_id": result.StoreID,
		"model_id": result.ModelID,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			return fmt.Errorf("write OpenFGA identifier file %s: %w", name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/openfga-provisioner/ -v`
Expected: PASS, with the existing stdout behavior unchanged.

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/openfga-provisioner/main.go cmd/openfga-provisioner/main_test.go
git add cmd/openfga-provisioner/
git commit -m "feat(openfga-provisioner): write identifier files for shared-volume handoff"
```

---

### Task 4: Consolidate four Postgres instances into one

Deliverable: the existing stack, unchanged in behavior, running against a single
Postgres server. Application containers do not exist yet — verification is via
the current host-based workflow.

**Files:**
- Create: `deploy/postgres/init.sh`
- Modify: `docker-compose.yaml`
- Modify: `.env.example`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: a `postgres` compose service on host port `55432`, hosting databases `tflive_test`, `keycloak`, `openfga`, `temporal`, and `temporal_visibility`. Task 7 points `api` and `worker` at it.

- [ ] **Step 1: Write the database init script**

Create `deploy/postgres/init.sh`. The Postgres image runs everything in
`/docker-entrypoint-initdb.d` on first initialization of an empty data
directory, so this executes exactly once per volume.

```sh
#!/bin/sh
# Creates one database per component on the shared local-development Postgres
# server. Runs only on first initialization of an empty data directory.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE ROLE "${KEYCLOAK_DB_USER}" LOGIN PASSWORD '${KEYCLOAK_DB_PASSWORD}';
	CREATE DATABASE "${KEYCLOAK_DB_NAME}" OWNER "${KEYCLOAK_DB_USER}";

	CREATE ROLE "${OPENFGA_DB_USER}" LOGIN PASSWORD '${OPENFGA_DB_PASSWORD}';
	CREATE DATABASE "${OPENFGA_DB_NAME}" OWNER "${OPENFGA_DB_USER}";

	CREATE ROLE "${TEMPORAL_DB_USER}" LOGIN PASSWORD '${TEMPORAL_DB_PASSWORD}';
	CREATE DATABASE "temporal" OWNER "${TEMPORAL_DB_USER}";
	CREATE DATABASE "temporal_visibility" OWNER "${TEMPORAL_DB_USER}";
EOSQL
```

Temporal's `auto-setup` creates databases itself when they are absent. Creating
both here and granting ownership lets its schema step proceed without the role
holding `CREATEDB`.

```bash
chmod +x deploy/postgres/init.sh
```

- [ ] **Step 2: Replace the four Postgres services with one**

In `docker-compose.yaml`, delete the `app-postgres`, `keycloak-postgres`,
`openfga-postgres`, and `temporal-postgres` services, and add:

```yaml
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${APP_DB_USER:-tflive}
      POSTGRES_PASSWORD: ${APP_DB_PASSWORD:-tflive}
      POSTGRES_DB: ${APP_DB_NAME:-tflive_test}
      KEYCLOAK_DB_USER: ${KEYCLOAK_DB_USER:-keycloak}
      KEYCLOAK_DB_PASSWORD: ${KEYCLOAK_DB_PASSWORD:-keycloak-local-only}
      KEYCLOAK_DB_NAME: ${KEYCLOAK_DB_NAME:-keycloak}
      OPENFGA_DB_USER: ${OPENFGA_DB_USER:-openfga}
      OPENFGA_DB_PASSWORD: ${OPENFGA_DB_PASSWORD:-openfga-local-only}
      OPENFGA_DB_NAME: ${OPENFGA_DB_NAME:-openfga}
      TEMPORAL_DB_USER: ${TEMPORAL_DB_USER:-temporal}
      TEMPORAL_DB_PASSWORD: ${TEMPORAL_DB_PASSWORD:-temporal}
    ports:
      - "55432:5432"
    volumes:
      - postgres-data:/var/lib/postgresql/data
      - ./deploy/postgres/init.sh:/docker-entrypoint-initdb.d/10-databases.sh:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${APP_DB_USER:-tflive} -d ${APP_DB_NAME:-tflive_test}"]
      interval: 5s
      timeout: 5s
      retries: 10
```

Replace the `volumes:` block at the bottom of the file with:

```yaml
volumes:
  postgres-data:
  minio-data:
  openfga-ids:
  artifacts:
```

`openfga-ids` and `artifacts` are consumed in Task 7.

- [ ] **Step 3: Repoint every consumer at the shared server**

Three edits in `docker-compose.yaml`:

In `keycloak`, change the `depends_on` key from `keycloak-postgres` to
`postgres`, and set:

```yaml
      KC_DB_URL: jdbc:postgresql://postgres:5432/${KEYCLOAK_DB_NAME:-keycloak}
```

In both `openfga-migrate` and `openfga`, change `depends_on` from
`openfga-postgres` to `postgres` and set the datastore URI in each:

```yaml
      OPENFGA_DATASTORE_URI: postgres://${OPENFGA_DB_USER:-openfga}:${OPENFGA_DB_PASSWORD:-openfga-local-only}@postgres:5432/${OPENFGA_DB_NAME:-openfga}?sslmode=disable
```

In `temporal`, change `depends_on` from `temporal-postgres` to `postgres` and set:

```yaml
      POSTGRES_SEEDS: postgres
      POSTGRES_USER: ${TEMPORAL_DB_USER:-temporal}
      POSTGRES_PWD: ${TEMPORAL_DB_PASSWORD:-temporal}
```

Every `depends_on` above keeps `condition: service_healthy`.

- [ ] **Step 4: Verify the consolidated stack from a clean volume**

```bash
docker compose --env-file .env.example down -v
docker compose --env-file .env.example up -d postgres
sleep 10
docker compose --env-file .env.example exec postgres psql -U tflive -d tflive_test -c "\l"
```

Expected: `tflive_test`, `keycloak`, `openfga`, `temporal`, and
`temporal_visibility` all listed, owned by their respective roles.

```bash
docker compose --env-file .env.example up -d keycloak keycloak-provision openfga-migrate openfga temporal
docker compose --env-file .env.example ps
```

Expected: `keycloak`, `openfga`, and `temporal` healthy or running;
`keycloak-provision` and `openfga-migrate` exited `0`.

- [ ] **Step 5: Record the new variables and commit**

Add `APP_DB_USER`, `APP_DB_PASSWORD`, `APP_DB_NAME`, `TEMPORAL_DB_USER`, and
`TEMPORAL_DB_PASSWORD` to `.env.example` next to the existing `*_DB_*` blocks,
with the same defaults used above and a comment noting all components now share
one server. Note in the same place that consolidating requires
`docker compose down -v` once for existing volumes.

```bash
git add deploy/postgres/init.sh docker-compose.yaml .env.example
git commit -m "refactor(compose): consolidate four Postgres instances into one"
```

---

### Task 5: Container images for api and worker

**Files:**
- Create: `Dockerfile.api`
- Create: `Dockerfile.worker`
- Create: `.dockerignore`

**Interfaces:**
- Consumes: `resolveOpenFGAIdentifier` behavior from Task 2 — these images read `OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE` at runtime.
- Produces: images whose entrypoints are `/usr/local/bin/tflive-api` and `/usr/local/bin/tflive-worker`. Task 7 builds both from compose.

- [ ] **Step 1: Add a .dockerignore**

Create `.dockerignore` so build context stays small and host artifacts never
leak into an image:

```
.git
.env
node_modules
web/node_modules
web/dist
tmp
api
keycloak-provisioner
.worktrees
docs
```

- [ ] **Step 2: Write Dockerfile.api**

The build stage copies the whole module because `cmd/api` imports most of
`internal/`, unlike the narrow provisioner images.

```dockerfile
FROM golang:1.24.1-alpine3.21 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tflive-api ./cmd/api

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S tflive \
    && adduser -S -D -H -G tflive tflive
COPY --from=build /out/tflive-api /usr/local/bin/tflive-api

USER tflive
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/tflive-api"]
```

- [ ] **Step 3: Write Dockerfile.worker**

The worker shells out to `tofu` (`internal/runner/terraform.go:30`) and clones
template sources with `git` (`internal/activities/template_run.go:63`), so both
must exist in the runtime image.

```dockerfile
FROM golang:1.24.1-alpine3.21 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tflive-worker ./cmd/worker

FROM alpine:3.21

ARG OPENTOFU_VERSION=1.10.6
ARG TARGETARCH

RUN apk add --no-cache ca-certificates git \
    && wget -qO /tmp/tofu.tar.gz \
        "https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.tar.gz" \
    && tar -xzf /tmp/tofu.tar.gz -C /usr/local/bin tofu \
    && rm /tmp/tofu.tar.gz \
    && addgroup -S tflive \
    && adduser -S -D -H -G tflive tflive
COPY --from=build /out/tflive-worker /usr/local/bin/tflive-worker

USER tflive
ENTRYPOINT ["/usr/local/bin/tflive-worker"]
```

If `OPENTOFU_VERSION` no longer exists upstream, bump the ARG default to the
current release rather than unpinning it.

- [ ] **Step 4: Verify both images build and the binaries run**

```bash
docker build -f Dockerfile.api -t tflive-api:dev .
docker build -f Dockerfile.worker -t tflive-worker:dev .
docker run --rm tflive-worker:dev --help 2>&1 | head -5 || true
docker run --rm --entrypoint tofu tflive-worker:dev version
docker run --rm --entrypoint git tflive-worker:dev --version
```

Expected: both images build; `tofu version` prints the pinned version; `git`
prints a version. The binaries exiting non-zero without configuration is fine —
the check is that they exist and are executable.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile.api Dockerfile.worker .dockerignore
git commit -m "build: add container images for api and worker"
```

---

### Task 6: Container image for the web UI

**Files:**
- Create: `Dockerfile.web`
- Create: `deploy/web/nginx.conf`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: an image serving the built SPA on port `5173` and proxying `/v1` and `/healthz` to `http://api:8081`. Task 7 runs it as the `web` service.

- [ ] **Step 1: Write the nginx config**

Create `deploy/web/nginx.conf`. The proxied paths mirror the Vite dev-server
proxy in `web/vite.config.ts`, so the browser sees one origin in both workflows.
The `try_files` fallback is required because the SPA uses client-side routing —
without it, a reload on `/auth/callback` returns 404 and login breaks.

```nginx
server {
    listen 5173;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;

    location /v1 {
        proxy_pass http://api:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /healthz {
        proxy_pass http://api:8081;
        proxy_set_header Host $host;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

- [ ] **Step 2: Write Dockerfile.web**

`VITE_*` values are compile-time in Vite, so they are build args rather than
runtime environment.

```dockerfile
FROM node:22-alpine3.21 AS build

ARG VITE_OIDC_ISSUER=http://keycloak.localhost:8082/realms/tflive
ARG VITE_OIDC_CLIENT_ID=tflive-web
ARG VITE_OIDC_REDIRECT_URI=http://localhost:5173/auth/callback
ARG VITE_TFLIVE_TENANT_ID=tenant_123
ENV VITE_OIDC_ISSUER=$VITE_OIDC_ISSUER \
    VITE_OIDC_CLIENT_ID=$VITE_OIDC_CLIENT_ID \
    VITE_OIDC_REDIRECT_URI=$VITE_OIDC_REDIRECT_URI \
    VITE_TFLIVE_TENANT_ID=$VITE_TFLIVE_TENANT_ID

WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM nginx:1.27-alpine

COPY deploy/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html

EXPOSE 5173
```

If `web/package-lock.json` is absent, generate it with `npm install` in `web/`
and commit it — `npm ci` requires a lockfile and gives reproducible builds.

- [ ] **Step 3: Verify the image builds and serves the SPA**

```bash
docker build -f Dockerfile.web -t tflive-web:dev .
docker run --rm -d --name web-probe -p 5173:5173 tflive-web:dev
sleep 3
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5173/
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5173/stacks
docker rm -f web-probe
```

Expected: `200` for both. The second confirms the SPA fallback — a deep link
must not 404.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.web deploy/web/nginx.conf
git commit -m "build: add container image for the web UI"
```

---

### Task 7: Wire the application services into compose

The task that makes `docker compose up` work end to end.

**Files:**
- Modify: `docker-compose.yaml`

**Interfaces:**
- Consumes: the `postgres` service from Task 4; images from Tasks 5 and 6; `OPENFGA_ID_OUTPUT_DIR` from Task 3; `OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE` from Task 2.
- Produces: a default `docker compose up` that serves the UI on `http://localhost:5173`.

- [ ] **Step 1: Make the OpenFGA provisioner bootstrap and publish identifiers**

Replace the `openfga-provision` service's `command` and add the volume and
output directory:

```yaml
  openfga-provision:
    build:
      context: .
      dockerfile: Dockerfile.openfga-provisioner
    command: ["bootstrap"]
    depends_on:
      openfga:
        condition: service_healthy
    environment:
      OPENFGA_API_URL: http://openfga:8080
      OPENFGA_STORE_NAME: tflive
      OPENFGA_ID_OUTPUT_DIR: /run/openfga
      OPENFGA_HTTP_TIMEOUT: ${OPENFGA_HTTP_TIMEOUT:-10s}
      OPENFGA_API_TOKEN: ${OPENFGA_API_TOKEN:-}
    volumes:
      - openfga-ids:/run/openfga
    restart: "no"
```

`bootstrap` replaces `verify` as the default because it converges: it reuses a
matching store and model and creates either only when absent
(`internal/openfga/provisioner.go:43-45` and `:64-66`). `OPENFGA_STORE_ID` and
`OPENFGA_MODEL_ID` are dropped from this service — bootstrap does not read them,
and leaving them invites a stale copy.

- [ ] **Step 2: Give Keycloak the aliased hostname**

In the `keycloak` service, replace the ports block and add the hostname settings
and network alias:

```yaml
    environment:
      KC_DB: postgres
      KC_DB_URL: jdbc:postgresql://postgres:5432/${KEYCLOAK_DB_NAME:-keycloak}
      KC_DB_USERNAME: ${KEYCLOAK_DB_USER:-keycloak}
      KC_DB_PASSWORD: ${KEYCLOAK_DB_PASSWORD:-keycloak-local-only}
      KC_BOOTSTRAP_ADMIN_USERNAME: ${KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME:-tflive-admin}
      KC_BOOTSTRAP_ADMIN_PASSWORD: ${KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD:-tflive-admin-local-only}
      KC_HTTP_PORT: "8082"
      KC_HOSTNAME: http://keycloak.localhost:8082
      KC_HEALTH_ENABLED: "true"
      KC_METRICS_ENABLED: "true"
    ports:
      - "8082:8082"
    networks:
      default:
        aliases:
          - keycloak.localhost
```

The published and internal ports must both be `8082`: the browser reaches
`keycloak.localhost:8082` through the published port, `api` reaches the same name
through the Docker DNS alias, and the port is part of the issuer string that
`internal/authn/oidc_provider.go:65` compares byte-for-byte.

Keycloak's healthcheck probes `localhost:9000`, the management port, which is
unaffected by `KC_HTTP_PORT`. Leave it as it is.

- [ ] **Step 3: Add the api, worker, and web services**

```yaml
  api:
    build:
      context: .
      dockerfile: Dockerfile.api
    depends_on:
      postgres:
        condition: service_healthy
      temporal:
        condition: service_started
      keycloak-provision:
        condition: service_completed_successfully
      openfga-provision:
        condition: service_completed_successfully
    environment:
      HTTP_ADDRESS: ":8081"
      DATABASE_URL: postgres://${APP_DB_USER:-tflive}:${APP_DB_PASSWORD:-tflive}@postgres:5432/${APP_DB_NAME:-tflive_test}?sslmode=disable
      TEMPORAL_ADDRESS: temporal:7233
      TEMPORAL_TASK_QUEUE: ${TEMPORAL_TASK_QUEUE:-terraform-runs}
      TFLIVE_ENVIRONMENT: development
      TFLIVE_TENANT_ID: ${TFLIVE_TENANT_ID:-tenant_123}
      CREDENTIAL_ENCRYPTION_KEY: ${CREDENTIAL_ENCRYPTION_KEY:?set CREDENTIAL_ENCRYPTION_KEY}
      OIDC_ISSUER_URL: http://keycloak.localhost:8082/realms/tflive
      OIDC_AUDIENCE: ${OIDC_AUDIENCE:-tflive-api}
      OPENFGA_API_URL: http://openfga:8080
      OPENFGA_STORE_ID_FILE: /run/openfga/store_id
      OPENFGA_MODEL_ID_FILE: /run/openfga/model_id
      ARTIFACT_STORE_KIND: filesystem
      ARTIFACT_STORE_FILESYSTEM_ROOT: /var/lib/tflive/artifacts
    ports:
      - "8081:8081"
    volumes:
      - openfga-ids:/run/openfga:ro
      - artifacts:/var/lib/tflive/artifacts

  worker:
    build:
      context: .
      dockerfile: Dockerfile.worker
    depends_on:
      postgres:
        condition: service_healthy
      temporal:
        condition: service_started
      openfga-provision:
        condition: service_completed_successfully
    environment:
      DATABASE_URL: postgres://${APP_DB_USER:-tflive}:${APP_DB_PASSWORD:-tflive}@postgres:5432/${APP_DB_NAME:-tflive_test}?sslmode=disable
      TEMPORAL_ADDRESS: temporal:7233
      TEMPORAL_TASK_QUEUE: ${TEMPORAL_TASK_QUEUE:-terraform-runs}
      TFLIVE_ENVIRONMENT: development
      TFLIVE_TENANT_ID: ${TFLIVE_TENANT_ID:-tenant_123}
      CREDENTIAL_ENCRYPTION_KEY: ${CREDENTIAL_ENCRYPTION_KEY:?set CREDENTIAL_ENCRYPTION_KEY}
      OIDC_ISSUER_URL: http://keycloak.localhost:8082/realms/tflive
      OIDC_AUDIENCE: ${OIDC_AUDIENCE:-tflive-api}
      OPENFGA_API_URL: http://openfga:8080
      OPENFGA_STORE_ID_FILE: /run/openfga/store_id
      OPENFGA_MODEL_ID_FILE: /run/openfga/model_id
      ARTIFACT_STORE_KIND: filesystem
      ARTIFACT_STORE_FILESYSTEM_ROOT: /var/lib/tflive/artifacts
      WORKER_RUN_ROOT: /var/lib/tflive/runs
    volumes:
      - openfga-ids:/run/openfga:ro
      - artifacts:/var/lib/tflive/artifacts

  web:
    build:
      context: .
      dockerfile: Dockerfile.web
    depends_on:
      - api
    ports:
      - "5173:5173"
```

`api` and `worker` share the `artifacts` volume because `api` reads the run logs
`worker` writes. `CREDENTIAL_ENCRYPTION_KEY` has no default on purpose — it is a
real key, and `.env.example` supplies a local-development value.

- [ ] **Step 4: Move MinIO and temporal-ui behind profiles**

Add `profiles: ["s3"]` to both `minio` and `minio-init`, and
`profiles: ["debug"]` to `temporal-ui`. Change nothing else about them. Both
stay reachable via `docker compose --profile s3 up` and
`docker compose --profile debug up`.

- [ ] **Step 5: Verify the whole stack from a clean checkout state**

```bash
docker compose down -v
docker compose --env-file .env.example up -d --build
docker compose --env-file .env.example ps
```

Expected: `postgres`, `keycloak`, `openfga`, `temporal`, `api`, `worker`, and
`web` up; the three one-shots exited `0`; neither MinIO nor `temporal-ui`
started.

```bash
curl -s http://localhost:8081/healthz
curl -s http://keycloak.localhost:8082/realms/tflive/.well-known/openid-configuration | head -c 200
docker compose --env-file .env.example logs api | grep -i "openfga\|error" | head
```

Expected: the API is healthy; the discovery document's `issuer` is exactly
`http://keycloak.localhost:8082/realms/tflive`; the API logs show no OpenFGA
configuration error, proving the file handoff worked.

Then open `http://localhost:5173`, log in as the platform admin from
`.env.example`, and confirm the UI loads authenticated.

- [ ] **Step 6: Verify idempotency and commit**

```bash
docker compose --env-file .env.example down
docker compose --env-file .env.example up -d
docker compose --env-file .env.example ps
```

Expected: everything returns healthy with no manual step, proving both
provisioners converge on a second run.

```bash
git add docker-compose.yaml
git commit -m "feat(compose): run the full stack with docker compose up"
```

---

### Task 8: Rewrite the quickstart

**Files:**
- Modify: `README.md` (the `## Local Development` section)
- Modify: `.env.example`

**Interfaces:**
- Consumes: the working stack from Task 7.
- Produces: documentation matching the shipped behavior.

- [ ] **Step 1: Replace the quickstart with the one-command path**

Rewrite `## Local Development` in `README.md` so it opens with:

````markdown
## Quickstart

Requires Docker. No Go or Node toolchain, and no `.env`.

```bash
docker compose up
```

The UI is at `http://localhost:5173`, the API at `http://localhost:8081`, and
Keycloak at `http://keycloak.localhost:8082`. Sign in with the platform
administrator credentials in `.env.example`.

Optional profiles:

```bash
docker compose --profile s3 up     # MinIO, for the S3 artifact adapter
docker compose --profile debug up  # Temporal UI on http://localhost:8080
```
````

Delete the two-phase OpenFGA bootstrap instructions and the identifier
copy-paste step entirely — they no longer describe how the system works.

- [ ] **Step 2: Document the host-based workflow as a second section**

Keep the existing `go run` and `npm run dev` instructions under a
`## Developing against the stack` heading, noting that `.env` overrides the
compose defaults and that `docker compose up postgres keycloak openfga temporal`
starts dependencies only. Note that `web` and `npm run dev` both bind `5173`, so
run one or the other.

- [ ] **Step 3: Document the one-time migration**

Add a short note that consolidating Postgres requires `docker compose down -v`
once for volumes created before this change, and that doing so discards local
data.

- [ ] **Step 4: Verify the README is accurate**

Follow the new quickstart literally, from `docker compose down -v` on a machine
with no `.env`:

```bash
mv .env .env.backup 2>/dev/null || true
docker compose down -v
docker compose up -d --build
```

Expected: a working authenticated UI with no other step. Restore `.env`
afterwards with `mv .env.backup .env`.

- [ ] **Step 5: Commit**

```bash
git add README.md .env.example
git commit -m "docs: rewrite the quickstart around docker compose up"
```

---

## Self-Review

**Spec coverage.** Every spec section maps to a task: Service Topology → 4 and 7;
Components → 5, 6, 7; Database Consolidation → 4; OpenFGA Identifier Handoff →
2 and 3; Issuer Resolution → 1 and 7 step 2; Configuration → 4 step 5 and 7 step
3; Startup Ordering → 7 step 3; Profiles → 7 step 4; Risks → 1; Verification →
the verify step of each task plus 7 steps 5-6 and 8 step 4.

**Gap found and closed.** The spec's Verification list requires `go test ./...`
and `npm test` to stay green, which no single task asserted. Tasks 2 and 3 run
their own packages; run the full suite before the final commit of Task 8.

**Type consistency.** `resolveOpenFGAIdentifier` is defined in Task 2 and
referenced only there. `writeIdentifierFiles` is defined and used only in Task
3. The file names `store_id` and `model_id` written in Task 3 match the
`OPENFGA_STORE_ID_FILE` and `OPENFGA_MODEL_ID_FILE` paths set in Task 7. The
`openfga-ids` and `artifacts` volumes declared in Task 4 are consumed in Task 7.
The issuer string is identical in Tasks 1, 6, and 7.
