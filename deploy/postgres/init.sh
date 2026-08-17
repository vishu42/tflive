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
