# Local authentication environment defaults

## Goal

Make the local `.env` authentication block predictable and easy to scan by aligning it with the repository's documented development defaults.

## Design

Replace the current interleaved Keycloak and OpenFGA values with the local-development values from `.env.example`. Organize the block in this order:

1. Keycloak database
2. Keycloak bootstrap administrator
3. Keycloak web client URLs
4. Keycloak platform administrator
5. OpenFGA database

Add short subgroup comments, remove the duplicate `KEYCLOAK_WEB_REDIRECT_URIS` entry, and retain an explicit warning that the credentials are only suitable for local development. Production deployments must continue supplying secrets through their deployment environment or secret manager.

## Defaults

Use the same hostnames, usernames, passwords, redirect URIs, web origins, and platform administrator identity documented in `.env.example`. Do not invent production-like credentials or change unrelated environment variables.

## Verification

- Each expected Keycloak and OpenFGA variable appears exactly once.
- The resulting values match `.env.example`.
- The block is grouped in the documented order.
- No lines outside the referenced local authentication block change.
