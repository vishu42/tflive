# Local Authentication Environment Defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the ungrouped local authentication variables in `.env` with the repository's complete, documented local-development defaults.

**Architecture:** Treat `.env.example` on the completed AUTH-004 branch as the source of truth. Replace only the selected authentication block, organizing variables by service responsibility and leaving generated OpenFGA identifiers blank so they must still be supplied explicitly after bootstrap.

**Tech Stack:** dotenv configuration, Docker Compose, Keycloak, OpenFGA

## Global Constraints

- Local credentials must match `.env.example` and must remain clearly labeled as local-development-only.
- `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` must not receive fabricated defaults; they remain blank until bootstrap emits exact values.
- Remove the duplicate `KEYCLOAK_WEB_REDIRECT_URIS` entry.
- Do not modify unrelated `.env` variables.
- `.env` is intentionally ignored and must not be staged or committed.

---

### Task 1: Normalize the local authentication block

**Files:**
- Modify: `.env:129-144`
- Reference: `.worktrees/auth-004-openfga-model/.env.example:46-85`

**Interfaces:**
- Consumes: Docker Compose's dotenv variable loading and the AUTH-004 provisioning contract.
- Produces: One complete local authentication block with unique Keycloak/OpenFGA keys and explicit blank generated identifiers.

- [ ] **Step 1: Confirm the current block fails the desired uniqueness and completeness checks**

Run:

```bash
rtk rg -c '^KEYCLOAK_WEB_REDIRECT_URIS=' .env
rtk rg -n '^OPENFGA_(STORE_ID|MODEL_ID|API_TOKEN|HTTP_TIMEOUT)=' .env
```

Expected: the first command prints `2`; the second returns no matches.

- [ ] **Step 2: Replace the selected block with the grouped defaults**

Use `apply_patch` to replace lines 129-144 with exactly:

```dotenv
# Authentication and authorization services (local development only)
# Production must provide credentials through a deployment secret manager.

# Keycloak database
KEYCLOAK_DB_NAME=keycloak
KEYCLOAK_DB_USER=keycloak
KEYCLOAK_DB_PASSWORD=keycloak-local-only

# Keycloak bootstrap administrator
KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME=tflive-admin
KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD=tflive-admin-local-only

# Keycloak web client
KEYCLOAK_WEB_REDIRECT_URIS=http://localhost:5173/,http://127.0.0.1:5173/
KEYCLOAK_WEB_ORIGINS=http://localhost:5173,http://127.0.0.1:5173

# Keycloak platform administrator
KEYCLOAK_PLATFORM_ADMIN_USERNAME=tflive-platform-admin
KEYCLOAK_PLATFORM_ADMIN_PASSWORD=tflive-platform-admin-local-only
KEYCLOAK_PLATFORM_ADMIN_EMAIL=tflive-platform-admin@local.test
KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME=tflive
KEYCLOAK_PLATFORM_ADMIN_LAST_NAME=Platform Administrator

# OpenFGA database
OPENFGA_DB_NAME=openfga
OPENFGA_DB_USER=openfga
OPENFGA_DB_PASSWORD=openfga-local-only

# OpenFGA provisioning
# Populate the exact IDs emitted by `docker compose run --rm openfga-provision bootstrap`.
OPENFGA_STORE_ID=
OPENFGA_MODEL_ID=
OPENFGA_API_TOKEN=
OPENFGA_HTTP_TIMEOUT=10s
```

- [ ] **Step 3: Verify every expected variable is present exactly once**

Run:

```bash
rtk awk -F= '/^(KEYCLOAK|OPENFGA)_/{count[$1]++} END{bad=0; for (key in count) if (count[key] != 1) {print key "=" count[key]; bad=1} exit bad}' .env
rtk rg -n '^(KEYCLOAK|OPENFGA)_' .env
```

Expected: the first command exits `0` with no output; the second lists the grouped variables once each.

- [ ] **Step 4: Verify defaults and Compose parsing**

Run:

```bash
rtk node -e 'const fs=require("fs"); const parse=p=>Object.fromEntries(fs.readFileSync(p,"utf8").split(/\r?\n/).filter(l=>/^[A-Z][A-Z0-9_]*=/.test(l)).map(l=>{const i=l.indexOf("=");return [l.slice(0,i),l.slice(i+1)]})); const actual=parse(".env"), expected=parse(".worktrees/auth-004-openfga-model/.env.example"); const keys=Object.keys(expected).filter(k=>k.startsWith("KEYCLOAK_")||k.startsWith("OPENFGA_")); const bad=keys.filter(k=>actual[k]!==expected[k]); if(bad.length){console.error(bad.join("\n"));process.exit(1)} console.log(`${keys.length} authentication defaults match`)'
rtk docker compose --env-file .env config --quiet
```

Expected: the comparison reports that all authentication defaults match; Compose exits `0` without output.

- [ ] **Step 5: Confirm the ignored local file remains the only implementation artifact**

Run:

```bash
rtk git status -sb
rtk git check-ignore -v .env
```

Expected: `.env` is identified by `.gitignore`; it is not staged or listed as a tracked change.
