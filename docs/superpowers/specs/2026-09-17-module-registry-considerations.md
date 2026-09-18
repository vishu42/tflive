# Private Terraform Module Registry — Considerations

Date: 2026-09-17
Status: outline, pre-design. Nothing here is decided.

## 1. What this is

Serve tflive's own modules over HashiCorp's [module registry
protocol](https://developer.hashicorp.com/terraform/internals/module-registry-protocol)
so that a `source = "registry.tflive.example/acme/vpc/aws"` line in someone's
Terraform resolves against us — both from a developer's laptop and from our own
executor during `tofu init`.

This is adjacent to, but not the same as, the existing template catalogue.
Templates are *things tflive runs for you*. Registry modules are *things other
people's Terraform consumes*. They can share a source of truth; they should not
share an identity model (see §6.1).

## 1b. Why build one at all, and what it competes with

Worth settling before anything else, because the honest answer is "often you
should not."

Unlike OCI, the module registry protocol standardises **consumption only**.
There are two endpoints and neither publishes; `terraform push` does not exist.
Every registry invents its own publish path (HashiCorp's public one uses GitHub
tag webhooks, TFC has a proprietary API, so does GitLab, so does Artifactory).
So we would be writing a bespoke publish path either way -- the protocol buys
us nothing there.

The real competitor is not a container registry, it is the `git::` source that
already works today with no registry at all:

```hcl
module "vpc" { source = "git::https://github.com/acme/tf-vpc.git?ref=v1.2.3" }
```

Three things a registry buys over that, verified 2026-09-17:

1. **Semver constraints.** `version = "~> 1.2"` is registry-only. A `git::`
   source takes a single `ref` -- branch, tag or SHA -- and nothing else. This
   is what the `versions` endpoint exists to feed, and it is the largest
   functional difference by some distance.
2. **Indirection.** `acme/vpc/aws` is a logical address. A `git::` URL bakes the
   repository location into every consumer, so moving orgs or forges becomes a
   migration across every repo that consumes us.
3. **Credential centralisation.** `git::` makes every consumer authenticate to
   the private source repo. A registry serving tarballs means only the registry
   does -- and we already hold a GitHub App installation token that consumers
   cannot. Strongest argument for tflive specifically; see §6.7.

**OpenTofu's `oci://` sources** are the push-standardised option and deserve an
explicit rejection rather than silence. They work against any OCI registry a
team already runs, but select by `?tag=` or `?digest=` with no `version`
constraint support, and HashiCorp Terraform does not implement them at all.
Since our executor runs `tofu`, this is available to us -- it is a real
alternative for the in-platform case, and a non-starter for any consumer on
Terraform proper.

| | version constraints | publish standardised | works on Terraform |
|---|---|---|---|
| `git::` | no | n/a | yes |
| registry protocol | yes | no | yes |
| `oci://` | no | yes | no (OpenTofu only) |

**The open strategic question.** In-platform consumption is already solved by
the template catalogue. A registry only earns its keep for Terraform running
*outside* tflive -- a laptop, someone's CI. That repositions tflive from "a
place you run infrastructure" to "a source of truth other people's Terraform
points at." That may be right, but it is a product decision that should be made
deliberately and not arrived at by building.

## 2. What the protocol actually requires

Smaller than it looks. Two endpoints and a discovery document.

**Discovery** — `GET https://<host>/.well-known/terraform.json`, served at the
host root, not under `/v1`:

```json
{ "modules.v1": "/v1/modules/", "providers.v1": "/v1/providers/" }
```

That is registry.terraform.io's own document, fetched 2026-09-17 — 62 bytes
total. The paths are relative, so they resolve against the discovery URL and
land on the same host. `modules.v1` may also be an absolute URL, which is what
would let discovery and the protocol endpoints live on different hosts (§6.5).
app.terraform.io does exactly this today, pointing `versions.v1` at
`https://checkpoint-api.hashicorp.com/`.

There is **no formal JSON Schema** for this document; the format is prose on the
[remote service discovery](https://developer.hashicorp.com/terraform/internals/remote-service-discovery)
page. `terraform.json` is registered with IANA as a well-known URI, but only
*provisionally* (2024-07-01), with the prose page as its reference.

One trap for any future consumer of a discovery document: values are not
uniformly URL strings. `login.v1`'s value is an object (`client`,
`grant_types`, `authz`, `token`, `ports`), so a `map[string]string` breaks on
any host that publishes it. Irrelevant while we only emit the document.

**List versions** — `GET <base>/:namespace/:name/:system/versions` → `200`:

```json
{ "modules": [ { "versions": [ {"version": "1.0.0"}, {"version": "1.1.0"} ] } ] }
```

`404` when the module does not exist. The real registry returns considerably
more per version — `source`, `root.providers`, `dependencies`, `submodules`,
`deprecation` — so extra fields are tolerated, but the CLI needs only
`version`.

**Download** — `GET <base>/:namespace/:name/:system/:version/download` → `204`
with an empty body and an `X-Terraform-Get` header pointing at the source. That
header takes any go-getter source: an absolute URL, a path relative to the
download URL, or a `git::` URL. Verified against the real registry on
2026-09-17 — it returns
`git::https://github.com/terraform-aws-modules/terraform-aws-vpc?ref=<sha>`,
i.e. a commit-pinned git source, *not* an archive. A presigned object-store URL
is equally legal. See §6.7, which this turns into a real decision.

Everything else on registry.terraform.io — search, list modules, module
metadata, submodules, README rendering — is *not* part of the protocol. The CLI
never calls it. We would only build it for our own UI, and our own UI can use
our own `/v1` API instead.

**Addressing** is `hostname/namespace/name/system`, where `system` is a
target-platform slug (`aws`, `azurerm`, `kubernetes`). It is mandatory in the
address and has no semantics we enforce beyond being part of the key.

**Auth** is a bearer token, sourced by the CLI from a `credentials` block in
`.tofurc` / `.terraformrc`, or from `TF_TOKEN_<host-with-dots-as-underscores>`
(hyphens double-underscore; non-ASCII punycoded). Env var wins over config file.
Both OpenTofu and Terraform implement this identically, which matters because
the executor runs `tofu`.

Version ordering is semver; the CLI does the constraint solving, we just list.

## 3. What tflive already has

- **Async ingest-from-git with validation**, end to end: `TemplateRegistration`
  → Temporal `template_sync` workflow → `internal/activities/template_sync.go`
  → `internal/runner/git.go` shallow-fetches one commit → variables extracted →
  revision goes `pending_validation` → `active`/`invalid`. A module publish is
  the same shape with a different output artifact.
- **Object storage behind an interface** — `internal/artifacts` with
  filesystem and S3 backends, already wired through `config.ArtifactStoreConfig`.
  Module tarballs belong here. S3 presigning is the natural `X-Terraform-Get`.
- **GitHub App credentials** — `internal/githubapp` mints short-lived
  installation tokens; `runner.GitCredential` carries them without leaking them
  into logs, argv, or `.git/config`.
- **OpenFGA authorization** with platform-level capabilities
  (`can_publish_template`, `can_read_template`) and per-object grants on `stack`.
  A `module` type with a `parent: [platform]` slots in the same way.
- **The executor's Terraform subprocess boundary** —
  `runner.LocalProcessRunner` already takes a per-run `Environment` map, which
  is exactly where `TF_TOKEN_*` would go.

## 4. The gaps, worst first

**4.1 There is no machine credential.** This is the whole problem. tflive
authenticates humans: OIDC login, an app-owned session, a cookie
(`internal/authn`). `grep` finds no API token, PAT, or service-account concept
anywhere in `internal/` or `cmd/`. The Terraform CLI will send
`Authorization: Bearer <opaque>` and nothing else — no cookie, no redirect, no
device flow unless we also implement `login.v1`. So a registry cannot ship
without first building a token type: issuance, storage (hashed), scope,
expiry, revocation, and a principal that `internal/authn/middleware.go` resolves
to alongside session principals. Everything else in this document is small next
to this.

**4.2 The executor needs to be a registry client.** `tofu init` inside a run
resolves `source = "<our-host>/..."`. That means the executor needs (a) the
token in its subprocess environment, (b) network reachability to the registry
host under the *same hostname the module source names*, and (c) TLS that
`tofu` trusts. Locally this collides with the existing split-horizon problem the
README already documents for Keycloak: `localhost:5173` in the browser is not
`localhost:5173` inside a container. A module source string is baked into
customer Terraform and cannot be rewritten per environment, so this needs a real
answer, not an env var we bend.

**4.3 Discovery lives at the host root.** `deploy/web/nginx.conf` proxies only
`/v1` and `/healthz`; everything else falls through to the SPA. A request for
`/.well-known/terraform.json` currently returns `index.html`. One new `location`
block, but it is a deployment-topology fact, not an application detail — anyone
fronting tflive differently has to replicate it.

**4.4 Versions are a new concept.** Templates are keyed by git ref → resolved
commit SHA, with a single `latest_template_revision_id` pointer. Registry
modules are keyed by semver, expose *all* versions simultaneously, and are
immutable once published. `TemplateRevision` cannot carry this without being
bent out of shape.

**4.5 Packaging — only if we choose it.** If `X-Terraform-Get` hands out a
tarball, something has to turn a resolved commit + `root_path` into one,
deterministically, with `.git` excluded, stored under a content-addressed key.
If it hands out a `git::` URL the way the public registry does, none of that
exists — but the client fetches the repo itself, which means every laptop needs
its own credential for a private repo. See §6.7.

## 4b. Fetch volume under concurrency

Raised 2026-09-17: what happens at 100 concurrent plans? Three separate
pressures, measured against the code rather than guessed.

**Provider downloads dominate, and are unrelated to GitHub.**
`TF_PLUGIN_CACHE_DIR` appears nowhere in the tree — not in Go, compose,
Dockerfiles or env files. Every `tofu init` therefore re-downloads providers
from registry.opentofu.org. At 100 concurrent AWS plans that is 100 × a
100+ MB provider. This dwarfs every other number here, is cheap to fix (a shared
cache volume, or a provider mirror), and is **independent of this project** — it
belongs in the backlog on its own.

**Template source fetches scale linearly with runs.**
`activities/template_run.go:176` runs `CheckoutCommit` unconditionally per run,
with no cache, against a commit that is immutable and was already fetched at
registration time (`template_sync.go:114`). So 100 plans is 100 fetches. They
are `--depth 1` single-commit fetches, so each is small. Note that git over
HTTPS does not consume GitHub's REST budget at all, so the realistic failure
mode is throttling and latency rather than a hard cliff — GitHub documents git
limits far less precisely than REST ones, which is itself a reason not to let
fetch volume scale linearly with runs.

**The REST numbers, for reference** (verified 2026-09-17). Unauthenticated:
60/hr per IP. PAT or user token: 5,000/hr, 15,000 on Enterprise Cloud. App
installation token: 5,000/hr baseline, +50/hr per repository beyond 20 and
+50/hr per user beyond 20, capped at 12,500/hr. Secondary limits cap concurrency
at 100 simultaneous REST/GraphQL requests and throughput at 900 points/min
(1 point per GET, 5 per mutation).

Against those, our one REST caller — token minting, once per repository per hour
— is noise at any volume. The figure that should worry us is the 60/hr
unauthenticated limit: `TokenSource.Token` returns an empty string when no
GitHub App is configured and the fetch proceeds unauthenticated by design, which
is fine at demo scale and a cliff for anyone running tflive at size without
configuring the App.

**Token minting is already solved.** `githubapp.TokenSource` caches per
repository under a mutex until five minutes before expiry, so concurrent runs
against one repository mint one token. That call *is* REST-rate-limited, and it
is the one already handled.

**Measured, 2026-09-17, on the local stack** (2 executor replicas, OpenTofu
1.12.5, container writable layer, 395 GB free).

One plan through tflive on the currently registered template
(`vishu42/terraform-templates`, root `vpc`): **4.9 s** wall, **344 KB**
workspace, **~20 KB** network. The whole run landed on one executor and the
other never moved, confirming the Temporal session pins a run to a host. But
that module declares no `required_providers` and no provider block — it is a
stub, so this is tflive's orchestration floor and says nothing about providers.

The same `tofu` binary against a realistic `hashicorp/aws ~> 5.0` module:

| configuration | init time | downloaded | workspace disk |
|---|---|---|---|
| no plugin cache (what tflive does today) | 36 s | ~154 MB | **642 MB** |
| `TF_PLUGIN_CACHE_DIR`, no lock file | 30 s | ~148 MB | 28 KB |
| `TF_PLUGIN_CACHE_DIR` + `.terraform.lock.hcl` | **1 s** | **0 B** | 28 KB |

Two things follow that were not obvious. First, the cache **symlinks** rather
than copies, so a workspace drops from 642 MB to 28 KB. Second, **the cache
alone does not stop the download**: without a lock file recording provider
hashes, OpenTofu still fetches to verify, so only the lock file turns this into
a 36x speedup and zero egress. Since every tflive run materialises a fresh
workspace from a git fetch, the lock file has to be committed in the module
repository or preserved by us — a cache volume on its own buys much less than
it appears to.

Extrapolating one burst of 100 concurrent plans on a real module:

| | today | cache + lock file |
|---|---|---|
| disk | ~64 GB, never reclaimed | ~645 MB |
| egress | ~15 GB | ~0 after the first |
| init latency | 36 s before plan starts | 1 s |

**Run workspaces are never reclaimed.** Verified directly: after the run above,
two run directories persist under `/var/lib/tflive/runs/tenant_123/`, one per
historical run, with no cleanup path anywhere. `EXECUTOR_RUN_ROOT` is not a
volume either, so this grows the container writable layer without bound. At
642 MB per run it is roughly 600 runs to fill this host.

**And GitHub is the smallest term by four orders of magnitude.** The git fetch
measured ~20 KB against ~154 MB of provider download — 0.013%. The original
worry that prompted this section turns out to be the one thing that does not
need fixing.

**Why this belongs in this document.** Packaging a revision once and serving it
from object storage turns the second pressure from once-per-run into
once-per-version, and object storage absorbs concurrency that GitHub's git
endpoints would rather we did not send. That is a stronger argument for tarballs
over `git::` passthrough (§6.7) than the credential argument. It is also a
change to the *run* path, with a wider blast radius than adding a read-only
endpoint, and it delivers the scale win with or without a registry — so it
should be costed separately rather than smuggled in as a registry benefit.

## 5. Sketch of the shape

**Domain** (`internal/domain/module.go`):

- `Module` — stable identity: `tenant_id`, `namespace`, `name`, `system`.
  Unique on the triple. Owns provenance (repo, root path) if published from git.
- `ModuleVersion` — `module_id`, `version` (semver), `resolved_commit_sha`,
  `artifact_key`, `artifact_sha256`, `size`, `status`, `published_by`,
  `published_at`. Immutable after `active`.
- `ModulePublication` — the async request record, mirroring
  `TemplateRegistration`.

**Publish flow**, reusing `template_sync`'s skeleton: API validates and writes a
`ModulePublication` → work queue → Temporal workflow → activity shallow-fetches
the commit via the GitHub App credential → packages `root_path` into a tarball →
puts it in `artifacts` → marks the version `active`. Failures land in
`error_summary` the same way registrations do.

**Serving** (`internal/api/registry.go`), unauthenticated-by-session,
token-authenticated:

- `GET /.well-known/terraform.json`
- `GET /v1/modules/{namespace}/{name}/{system}/versions`
- `GET /v1/modules/{namespace}/{name}/{system}/{version}/download`

Plus one non-protocol route the `download` handler points at when the artifact
store is the filesystem backend and cannot presign:
`GET /v1/modules/.../{version}/archive.tar.gz`.

**Authorization**: a `module` type in the FGA model with `parent: [platform]`,
`can_publish_module` / `can_read_module` capabilities, resolved for a token
principal exactly as for a user principal.

**Executor**: the run's environment gains `TF_TOKEN_<host>` for a
short-lived, run-scoped token, redacted through the same scrubbing path
`GitCredential` already uses.

## 6. Open decisions

**6.1 Is a registry module the same object as a template?** Three positions:
one object with two faces; two objects with a shared publish pipeline; two
objects, fully separate. The middle one is probably right — the *ingest* is
genuinely identical, the *identity* genuinely is not — but this is the call that
shapes everything else and it should be made explicitly.

**6.2 Where do versions come from?** Git tags (`v1.2.3`), matching how the
public registry works and how people expect to publish; or explicit
version-on-publish, which is simpler and needs no tag-scanning. Tags imply a
webhook or a poll to notice new ones.

**6.3 What kind of token, and how does it reach a laptop?** Long-lived
user-scoped PAT (what a laptop needs) and short-lived run-scoped token (what the
executor needs) are different products. Building only the first and reusing it
for runs would put a durable credential into every Terraform subprocess.

Distribution is a second axis. The cheap path is copy-paste: generate a PAT in
the UI, paste it into `.tofurc` or `TF_TOKEN_registry_tflive_example`. The
ergonomic path is implementing `login.v1`, which makes
`tofu login registry.tflive.example` run an OAuth authorization-code flow
(local redirect listener on ports 10000-10010) and write the token into the
CLI's own credentials store. That requires tflive to *be* an OAuth
authorization server, which it is not — it is an OIDC client to Keycloak today.
So this is either a real build or a delegation question: can Keycloak serve as
the `authz`/`token` endpoints for a `terraform-cli` public client, with tflive
exchanging the result for its own token? Worth answering before §7 step 1 is
scoped.

**6.4 Read access granularity.** Platform-wide `can_read_module`, or per-module
grants like `stack` has? Per-module is more machinery than a first cut needs,
but retrofitting object-level checks onto a shipped protocol endpoint is
unpleasant.

**6.5 Does the registry host = the API host?** Bundling it into the existing
API is less to run. A separate hostname makes 4.2's addressing problem tractable
and keeps a public-ish, token-authenticated surface off the session-cookie
origin.

**6.6 Anything before the tarball?** Validating that the module *parses* is
cheap and we already do it for templates. Verifying it *plans* is not, and is
probably out of scope.

**6.7 Tarball or `git::` passthrough?** The protocol permits either, and the
public registry chose `git::` with a pinned SHA. Passthrough is dramatically
less to build — no packaging, no object-store keys, no presigning, and the
registry degenerates into a lookup table over commit SHAs. It fails on the point
of the exercise though: a `git::` URL makes the *client* authenticate to the
source repository, so our GitHub App installation token is no help and every
consumer needs their own access to a private repo. Tarballs keep the credential
on our side, which is the argument for them — not the protocol, which does not
care. Tarballs also collapse per-run GitHub fetches into per-version ones; see
§4b.

## 7. Phasing, if it proceeds

1. Machine tokens (§4.1) — standalone, useful on its own, blocks everything.
2. Domain + publish pipeline + packaging, no serving. Publish by explicit
   version; tags later.
3. The three protocol endpoints + nginx `location` + token auth. A laptop can
   now `tofu init` against us.
4. Executor integration (§4.2), including whatever the hostname answer turns
   out to be.
5. UI: browse modules, copy the source string, see versions.

## 8. Explicitly out of scope

Provider registry protocol (a separate, larger spec with GPG signing).
`login.v1` / OAuth authorization-code flow -- deferred rather than rejected; see §6.3. Search and module metadata APIs. Public
(unauthenticated) modules. Mirroring or proxying the upstream registry. Module
deprecation/yanking — though `deprecation` is a real field in the versions
response, so this is a field we return null in, not one we omit.
