#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const rendered = execFileSync(
  "docker",
  [
    "compose",
    "--env-file",
    ".env.example",
    "-f",
    "docker-compose.yaml",
    "config",
    "--format",
    "json",
  ],
  { cwd: root, encoding: "utf8" },
);
const config = JSON.parse(rendered);
const source = readFileSync(resolve(root, "docker-compose.yaml"), "utf8");
const envExample = readFileSync(resolve(root, ".env.example"), "utf8");

function envValue(name) {
  const prefix = `${name}=`;
  const matches = envExample
    .split(/\r?\n/)
    .filter((line) => line.startsWith(prefix));
  assert.equal(matches.length, 1, `${name} must appear exactly once in .env.example`);
  return matches[0].slice(prefix.length);
}

for (const [name, value] of Object.entries({
  TFLIVE_ENVIRONMENT: "development",
  TFLIVE_TENANT_ID: "tenant_123",
  OIDC_ISSUER_URL: "http://keycloak.localhost:8082/realms/tflive",
  TFLIVE_PUBLIC_URL: "http://localhost:5173",
  OIDC_CLIENT_ID: "tflive-api",
})) {
  assert.equal(envValue(name), value, `${name} has the wrong local example value`);
}

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

const postgres = service("postgres");
const keycloak = service("keycloak");
const keycloakProvision = service("keycloak-provision");
const api = service("api");

assert.equal(postgres.image, "postgres:16-alpine");
assert.equal(keycloak.image, "quay.io/keycloak/keycloak:26.6.3");

assert.ok(postgres.healthcheck, "the shared Postgres needs a health check");
assert.ok(keycloak.healthcheck?.test?.join(" ").includes("/health/ready"));
assert.equal(keycloak.depends_on?.postgres?.condition, "service_healthy");
assert.equal(keycloakProvision.depends_on?.keycloak?.condition, "service_healthy");
assert.equal(keycloakProvision.restart, "no");
assert.equal(keycloakProvision.build?.dockerfile, "Dockerfile.keycloak-provisioner");
assert.deepEqual(keycloakProvision.ports ?? [], []);
assert.equal(keycloakProvision.environment?.KEYCLOAK_ADMIN_URL, "http://keycloak:8082");
assert.equal(keycloak.environment?.KC_HTTP_PORT, "8082");
assert.equal(keycloak.environment?.KC_HOSTNAME, "http://keycloak.localhost:8082");
assert.ok(
  keycloak.networks?.default?.aliases?.includes("keycloak.localhost"),
  "keycloak needs the keycloak.localhost alias so one issuer string resolves from both sides",
);
assert.equal(
  keycloakProvision.environment?.TFLIVE_PUBLIC_URL,
  envValue("TFLIVE_PUBLIC_URL"),
  "keycloak-provision must derive the client's redirect and post-logout URIs from the same TFLIVE_PUBLIC_URL as the API",
);
assert.equal(
  keycloakProvision.environment?.OIDC_CLIENT_SECRET,
  envValue("OIDC_CLIENT_SECRET"),
  "keycloak-provision must register the same client secret the API authenticates with",
);
assert.equal(
  api.depends_on?.["keycloak-provision"]?.condition,
  "service_completed_successfully",
  "the API must not start before the realm and client it authenticates against exist",
);

// TFLIVE_PUBLIC_URL is the one value all three parties to the OIDC handshake
// must agree on: the API derives its redirect and post-logout URIs from it,
// and the Keycloak provisioner registers those same URIs on the client. A
// stale default in any one of these three spots would only surface at login
// time as invalid_redirect_uri, so pin every "${TFLIVE_PUBLIC_URL:-...}"
// default in Compose to the .env.example value.
const publicURLDefaults = [
  ...source.matchAll(/\$\{TFLIVE_PUBLIC_URL:-([^}]*)\}/g),
].map((match) => match[1]);
assert.equal(
  publicURLDefaults.length,
  3,
  "expected TFLIVE_PUBLIC_URL to default in keycloak-provision, api, and worker",
);
for (const value of publicURLDefaults) {
  assert.equal(
    value,
    envValue("TFLIVE_PUBLIC_URL"),
    "every TFLIVE_PUBLIC_URL default in Compose must match .env.example, or the derived redirect URI can drift from what Keycloak has registered",
  );
}

// OpenFGA is embedded in the API. A service, a provisioner, or a required
// store or model identifier coming back would reintroduce the two-phase
// startup the single Compose file exists to remove.
assert.deepEqual(
  Object.keys(config.services).filter((name) => name.includes("openfga")),
  [],
  "OpenFGA runs inside the API; Compose must not start it as a service",
);
assert.doesNotMatch(
  source,
  /OPENFGA_(STORE_ID|MODEL_ID|API_URL|API_TOKEN)/,
  "the API resolves its OpenFGA store and model in process; Compose must not pass identifiers or a URL",
);

assert.ok(hasVolume(postgres, "postgres-data"));
assert.ok(config.volumes?.["postgres-data"]);

for (const name of [
  "OIDC_CLIENT_SECRET",
  "SESSION_ENCRYPTION_KEY",
  "KEYCLOAK_DB_NAME",
  "KEYCLOAK_DB_USER",
  "KEYCLOAK_DB_PASSWORD",
  "KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME",
  "KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD",
  "KEYCLOAK_PLATFORM_ADMIN_USERNAME",
  "KEYCLOAK_PLATFORM_ADMIN_PASSWORD",
  "KEYCLOAK_PLATFORM_ADMIN_EMAIL",
  "KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME",
  "KEYCLOAK_PLATFORM_ADMIN_LAST_NAME",
]) {
  assert.match(
    source,
    new RegExp(`\\$\\{${name}:-`),
    `${name} must have an inline default so the stack runs without .env`,
  );
}

const provisionerDockerfile = resolve(root, "Dockerfile.keycloak-provisioner");
assert.ok(existsSync(provisionerDockerfile), "missing provisioner Dockerfile");
const provisionerImage = readFileSync(provisionerDockerfile, "utf8");
assert.match(provisionerImage, /^FROM golang:1\.25\.14-alpine3\.23 AS build/m);
assert.match(provisionerImage, /^FROM alpine:3\.21$/m);
assert.match(provisionerImage, /^USER keycloak-provisioner$/m);

console.log("authentication Compose contract verified");
