#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
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
const keycloakProvision = service("keycloak-provision");
const openfgaPostgres = service("openfga-postgres");
const openfgaMigrate = service("openfga-migrate");
const openfga = service("openfga");
const openfgaProvision = service("openfga-provision");

assert.equal(keycloakPostgres.image, "postgres:16-alpine");
assert.equal(keycloak.image, "quay.io/keycloak/keycloak:26.6.3");
assert.equal(openfgaPostgres.image, "postgres:16-alpine");
assert.equal(openfgaMigrate.image, "openfga/openfga:v1.15.1");
assert.equal(openfga.image, "openfga/openfga:v1.15.1");

assert.ok(keycloakPostgres.healthcheck, "Keycloak Postgres needs a health check");
assert.ok(keycloak.healthcheck?.test?.join(" ").includes("/health/ready"));
assert.equal(keycloak.depends_on?.["keycloak-postgres"]?.condition, "service_healthy");
assert.equal(keycloakProvision.depends_on?.keycloak?.condition, "service_healthy");
assert.equal(keycloakProvision.restart, "no");
assert.equal(keycloakProvision.build?.dockerfile, "Dockerfile.keycloak-provisioner");
assert.deepEqual(keycloakProvision.ports ?? [], []);
assert.equal(keycloakProvision.environment?.KEYCLOAK_ADMIN_URL, "http://keycloak:8080");
assert.equal(
  keycloakProvision.environment?.KEYCLOAK_WEB_REDIRECT_URIS,
  "http://localhost:5173/,http://127.0.0.1:5173/",
);
assert.equal(
  keycloakProvision.environment?.KEYCLOAK_WEB_ORIGINS,
  "http://localhost:5173,http://127.0.0.1:5173",
);
assert.ok(openfgaPostgres.healthcheck, "OpenFGA Postgres needs a health check");
assert.equal(openfgaMigrate.depends_on?.["openfga-postgres"]?.condition, "service_healthy");
assert.equal(openfga.depends_on?.["openfga-migrate"]?.condition, "service_completed_successfully");
assert.ok(openfga.healthcheck?.test?.join(" ").includes("grpc_health_probe"));
assert.equal(openfgaProvision.depends_on?.openfga?.condition, "service_healthy");
assert.equal(openfgaProvision.restart, "no");
assert.equal(openfgaProvision.build?.dockerfile, "Dockerfile.openfga-provisioner");
assert.equal(resolve(root, openfgaProvision.build?.context ?? "__missing__"), root);
assert.deepEqual(openfgaProvision.ports ?? [], []);
assert.deepEqual(openfgaProvision.command, ["verify"]);
assert.equal(openfgaProvision.environment?.OPENFGA_API_URL, "http://openfga:8080");
assert.equal(openfgaProvision.environment?.OPENFGA_STORE_NAME, "tflive");
assert.equal(openfgaProvision.environment?.OPENFGA_STORE_ID, "");
assert.equal(openfgaProvision.environment?.OPENFGA_MODEL_ID, "");
assert.equal(openfgaProvision.environment?.OPENFGA_API_TOKEN, "");
assert.equal(openfgaProvision.environment?.OPENFGA_HTTP_TIMEOUT, "10s");

for (const token of [
  "${OPENFGA_STORE_ID:-}",
  "${OPENFGA_MODEL_ID:-}",
  "${OPENFGA_API_TOKEN:-}",
  "${OPENFGA_HTTP_TIMEOUT:-10s}",
]) {
  assert.ok(source.includes(token), `${token} must remain explicit`);
}

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
  "KEYCLOAK_PLATFORM_ADMIN_USERNAME",
  "KEYCLOAK_PLATFORM_ADMIN_PASSWORD",
  "KEYCLOAK_PLATFORM_ADMIN_EMAIL",
  "KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME",
  "KEYCLOAK_PLATFORM_ADMIN_LAST_NAME",
  "OPENFGA_DB_NAME",
  "OPENFGA_DB_USER",
  "OPENFGA_DB_PASSWORD",
]) {
  assert.match(source, new RegExp(`\\$\\{${name}:\\?`), `${name} must be required`);
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
  "COPY openfga ./openfga",
  "COPY internal/openfga ./internal/openfga",
  "COPY cmd/openfga-provisioner ./cmd/openfga-provisioner",
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
  /^[ \t]*&& adduser -S -D -H -G openfga-provisioner openfga-provisioner$/m,
);
assert.match(
  openfgaProvisionerImage,
  /^COPY --from=build \/out\/openfga-provisioner \/usr\/local\/bin\/openfga-provisioner$/m,
);
assert.match(openfgaProvisionerImage, /^USER openfga-provisioner$/m);
assert.match(
  openfgaProvisionerImage,
  /^ENTRYPOINT \["\/usr\/local\/bin\/openfga-provisioner"\]$/m,
);

console.log("authentication Compose contract verified");
