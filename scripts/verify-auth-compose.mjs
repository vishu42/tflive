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
    "-f",
    "docker-compose.app.yaml",
    "config",
    "--format",
    "json",
  ],
  { cwd: root, encoding: "utf8" },
);
const config = JSON.parse(rendered);
const source =
  readFileSync(resolve(root, "docker-compose.yaml"), "utf8") +
  readFileSync(resolve(root, "docker-compose.app.yaml"), "utf8");
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
  OIDC_ISSUER_URL: "http://tflive.localhost:8082/realms/tflive",
  OIDC_AUDIENCE: "tflive-api",
  OPENFGA_API_URL: "http://localhost:8083",
  OPENFGA_STORE_ID: "",
  OPENFGA_MODEL_ID: "",
  OPENFGA_API_TOKEN: "",
  OPENFGA_HTTP_TIMEOUT: "10s",
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
const openfgaMigrate = service("openfga-migrate");
const openfga = service("openfga");
const openfgaProvision = service("openfga-provision");

assert.equal(postgres.image, "postgres:16-alpine");
assert.equal(keycloak.image, "quay.io/keycloak/keycloak:26.6.3");
assert.equal(openfgaMigrate.image, "openfga/openfga:v1.15.1");
assert.equal(openfga.image, "openfga/openfga:v1.15.1");

assert.ok(postgres.healthcheck, "the shared Postgres needs a health check");
assert.ok(keycloak.healthcheck?.test?.join(" ").includes("/health/ready"));
assert.equal(keycloak.depends_on?.postgres?.condition, "service_healthy");
assert.equal(keycloakProvision.depends_on?.keycloak?.condition, "service_healthy");
assert.equal(keycloakProvision.restart, "no");
assert.equal(keycloakProvision.build?.dockerfile, "Dockerfile.keycloak-provisioner");
assert.deepEqual(keycloakProvision.ports ?? [], []);
assert.equal(keycloakProvision.environment?.KEYCLOAK_ADMIN_URL, "http://keycloak:8082");
assert.equal(keycloak.environment?.KC_HTTP_PORT, "8082");
assert.equal(keycloak.environment?.KC_HOSTNAME, "http://tflive.localhost:8082");
assert.ok(
  keycloak.networks?.default?.aliases?.includes("tflive.localhost"),
  "keycloak needs the tflive.localhost alias so one issuer string resolves from both sides",
);
assert.equal(
  keycloakProvision.environment?.KEYCLOAK_WEB_REDIRECT_URIS,
  "http://localhost:5173/auth/callback,http://127.0.0.1:5173/auth/callback",
);
assert.equal(
  keycloakProvision.environment?.KEYCLOAK_WEB_ORIGINS,
  "http://localhost:5173,http://127.0.0.1:5173",
);
assert.equal(openfgaMigrate.depends_on?.postgres?.condition, "service_healthy");
assert.equal(openfga.depends_on?.["openfga-migrate"]?.condition, "service_completed_successfully");
assert.ok(openfga.healthcheck?.test?.join(" ").includes("grpc_health_probe"));
assert.equal(openfgaProvision.depends_on?.openfga?.condition, "service_healthy");
assert.equal(openfgaProvision.restart, "no");
assert.equal(openfgaProvision.build?.dockerfile, "Dockerfile.openfga-provisioner");
assert.equal(resolve(root, openfgaProvision.build?.context ?? "__missing__"), root);
assert.deepEqual(openfgaProvision.ports ?? [], []);
assert.deepEqual(openfgaProvision.command, ["bootstrap"]);
assert.equal(openfgaProvision.environment?.OPENFGA_ID_OUTPUT_DIR, "/run/openfga");
assert.equal(openfgaProvision.environment?.OPENFGA_API_URL, "http://openfga:8080");
assert.equal(openfgaProvision.environment?.OPENFGA_STORE_NAME, "tflive");
assert.equal(openfgaProvision.environment?.OPENFGA_API_TOKEN, "");
assert.equal(openfgaProvision.environment?.OPENFGA_HTTP_TIMEOUT, "10s");

for (const token of [
  "${OPENFGA_API_TOKEN:-}",
  "${OPENFGA_HTTP_TIMEOUT:-10s}",
]) {
  assert.ok(source.includes(token), `${token} must remain explicit`);
}

assert.ok(hasVolume(postgres, "postgres-data"));
assert.ok(config.volumes?.["postgres-data"]);
assert.ok(config.volumes?.["openfga-ids"]);

for (const name of [
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
  "OPENFGA_DB_NAME",
  "OPENFGA_DB_USER",
  "OPENFGA_DB_PASSWORD",
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
assert.match(provisionerImage, /^FROM golang:1\.24\.1-alpine3\.21 AS build/m);
assert.match(provisionerImage, /^FROM alpine:3\.21$/m);
assert.match(provisionerImage, /^USER keycloak-provisioner$/m);

const openfgaProvisionerDockerfile = resolve(root, "Dockerfile.openfga-provisioner");
assert.ok(existsSync(openfgaProvisionerDockerfile), "missing OpenFGA provisioner Dockerfile");
const openfgaProvisionerImage = readFileSync(openfgaProvisionerDockerfile, "utf8");
assert.match(openfgaProvisionerImage, /^FROM golang:1\.24\.1-alpine3\.21 AS build/m);
const runtimeStageMarker = /^FROM alpine:3\.21$/m;
assert.match(openfgaProvisionerImage, runtimeStageMarker);
const [openfgaProvisionerBuildStage] = openfgaProvisionerImage.split(runtimeStageMarker);
const openfgaProvisionerBuildCopies =
  openfgaProvisionerBuildStage.match(/^COPY[ \t]+.*$/gm) ?? [];
assert.deepEqual(openfgaProvisionerBuildCopies, [
  "COPY go.mod go.sum ./",
  "COPY . .",
]);
assert.match(openfgaProvisionerImage, /^RUN CGO_ENABLED=0 go build /m);
assert.match(openfgaProvisionerImage, /^RUN [^\n]* -trimpath(?: |$)/m);
assert.match(openfgaProvisionerImage, /^RUN [^\n]* -ldflags="-s -w"(?: |$)/m);
assert.match(
  openfgaProvisionerImage,
  /^RUN [^\n]* -o \/out\/openfga-provisioner \.\/cmd\/openfga-provisioner$/m,
);
assert.match(openfgaProvisionerImage, /^RUN apk add --no-cache ca-certificates \\$/m);
assert.match(openfgaProvisionerImage, /^[ \t]*&& addgroup -S openfga-provisioner \\$/m);
assert.match(
  openfgaProvisionerImage,
  /^[ \t]*&& adduser -S -D -H -G openfga-provisioner openfga-provisioner( \\)?$/m,
);
assert.match(
  openfgaProvisionerImage,
  /^COPY --from=build \/out\/openfga-provisioner \/usr\/local\/bin\/openfga-provisioner$/m,
);
assert.match(openfgaProvisionerImage, /^USER openfga-provisioner$/m);
assert.match(
  openfgaProvisionerImage,
  /mkdir -p \/run\/openfga/,
  "the identifier directory must exist in the image so the volume mount inherits its ownership",
);
assert.match(
  openfgaProvisionerImage,
  /chown -R openfga-provisioner:openfga-provisioner \/run\/openfga/,
  "the provisioner user must own the identifier directory",
);
assert.match(
  openfgaProvisionerImage,
  /^ENTRYPOINT \["\/usr\/local\/bin\/openfga-provisioner"\]$/m,
);

console.log("authentication Compose contract verified");
