# Local development

Instructions for working on tflive itself. To simply run it, see the
[README](../README.md).

## Running the app on the host

Run the API, worker, and UI as host processes against the containerised
dependencies. This gives you fast rebuilds and a debugger.

Start the dependencies and provision them:

```bash
cp .env.example .env
docker compose up -d --wait
```

Copy the two identifiers the provisioner prints into `.env`:

```bash
docker compose logs openfga-provision
```

Compose does not inject `.env` into containers or host processes, so the API
and worker read those values from your shell. Load them and start each process
in its own terminal:

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
`5173`, the same port the `web` container uses, so run one or the other rather
than both.

`npm run dev` binds and prints `http://127.0.0.1:5173` — exactly how someone
hits the login trap below. Open `http://localhost:5173` instead: the redirect
URI is derived from a single `TFLIVE_PUBLIC_URL`, so only that exact origin is
registered with Keycloak, and `127.0.0.1` fails sign-in with "Invalid
parameter: redirect_uri".

## The Keycloak hostname

Keycloak is reached at `http://keycloak.localhost:8082` from both the browser
and from inside containers. That name resolves to loopback for the browser and
to the container through Docker's DNS, which is what lets a single issuer string
satisfy both — the API compares the discovery document's issuer to
`OIDC_ISSUER_URL` byte-for-byte, so the two cannot differ.

One trap worth knowing: `curl` resolves `*.localhost` to its own loopback and
ignores the Docker alias, so a `curl` test from inside a container fails
misleadingly. Use `wget` or a language HTTP client instead.

## Checking the OpenFGA identifiers

`verify` reads the pair from your environment and checks it against the live
store and model without mutating either:

```bash
docker compose run --rm openfga-provision verify
```

Note that the API does not check the pair at startup — it starts, and
authorization fails closed at request time. `verify` is what catches a wrong or
stale pair up front.

`bootstrap` reuses a store whose name already matches and a semantically equal
authorization model, creating either only when absent, so re-running it
converges rather than duplicating. Run only one at a time: OpenFGA store names
are not unique, and bootstrap fails closed if the `tflive` name is ambiguous.

## The authorization model

`openfga/authorization-model.fga` is the source of truth and the only form of
the model in the repository. Edit and review permission changes there — the DSL
is the form in which a wrong permission is visible.

`cmd/openfga-provisioner` embeds the DSL and transforms it to OpenFGA's API wire
format in process, so there is nothing to regenerate after a change and no
generated artifact that can drift. A malformed DSL fails
`go test ./openfga/...` rather than waiting for a provisioner run.

The transform rejects a syntax error but not a reference to a relation that
does not exist, so `define can_view: ownr` would transform cleanly and be
refused by OpenFGA at write time. The permission matrix in
`openfga/authorization-model-tests.fga.yaml` is what catches that, along with
every permission that is merely wrong. Run it on any model change; it needs the
[`fga` CLI](https://github.com/openfga/cli) on `PATH`:

```bash
cd openfga && fga model test --tests authorization-model-tests.fga.yaml
```

## Tests

```bash
go test ./...
gofmt -l cmd internal
node scripts/verify-auth-compose.mjs
```

```bash
cd web
npm test
npm run build
```

## Layout

```text
cmd/                  API, worker, and provisioner entry points
internal/app/         application use cases and ports
internal/api/         HTTP transport
internal/postgres/    product persistence and migrations
internal/queue/       durable work queue
internal/temporal/    Temporal client adapter
internal/workflows/   deterministic workflows
internal/activities/  side-effecting Temporal activities
internal/runner/      OpenTofu execution
internal/authn/       token verification
internal/authz/       authorization decisions
internal/openfga/     OpenFGA model and adapter
internal/keycloak/    realm provisioning (local demo IdP only)
web/                  Vite UI
```
