#!/bin/sh
# Creates one database per component on the shared local-development Postgres
# server. Runs only on first initialization of an empty data directory.
#
# OpenFGA has no entry here: it is embedded in the API and its tables live in
# the application database, created by the API at startup. That is what lets a
# tuple write join the same transaction as the domain write that caused it.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE ROLE "${KEYCLOAK_DB_USER}" LOGIN PASSWORD '${KEYCLOAK_DB_PASSWORD}';
	CREATE DATABASE "${KEYCLOAK_DB_NAME}" OWNER "${KEYCLOAK_DB_USER}";

	CREATE ROLE "${TEMPORAL_DB_USER}" LOGIN PASSWORD '${TEMPORAL_DB_PASSWORD}';
	CREATE DATABASE "temporal" OWNER "${TEMPORAL_DB_USER}";
	CREATE DATABASE "temporal_visibility" OWNER "${TEMPORAL_DB_USER}";
EOSQL
