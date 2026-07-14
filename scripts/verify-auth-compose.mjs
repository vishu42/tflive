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
