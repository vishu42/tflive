# Local State Hydration and MinIO Design

## Goal

Make the local end-to-end workflow durable across page refreshes by loading stacks and templates from the API/DB, and make local artifact storage use MinIO through the existing S3-compatible artifact adapter.

## Scope

- Add a local MinIO service to `docker-compose.yaml`.
- Add local MinIO/S3 artifact values to `.env` and `.env.example`.
- Add tenant-scoped list endpoints for stacks and templates.
- Update the React UI so stack and template controls are backed by API state instead of only current browser session state.

## Backend Design

The app service gains small list use cases:

- `ListStacks(ctx, tenantID)` returns tenant-owned `traits.Stack` records ordered newest first.
- `ListTemplates(ctx, tenantID)` returns tenant-owned `traits.Template` records ordered newest first.

The Postgres store implements those list methods using existing tables. The HTTP API exposes:

- `GET /v1/tenants/{tenant_id}/stacks`
- `GET /v1/tenants/{tenant_id}/templates`

Both endpoints return empty arrays when no rows exist and use existing tenant isolation by filtering on `tenant_id`.

## UI Design

On initial render and whenever tenant changes, the UI loads stacks and templates from the API. The Stack panel and Template panel each get a select control:

- Selecting a stack calls the existing stack detail endpoint and updates installed templates.
- Selecting a template loads variables through the existing template variables endpoint.
- Creating a stack refreshes the stack list and selects the created stack.
- Registering a template continues polling the registration; once complete, the UI refreshes templates, selects the registered template, and loads variables.

Install uses the selected stack and selected template, so rows already present in the DB are usable after refresh.

## Local MinIO Design

`docker-compose.yaml` adds:

- `minio`, exposing API port `9000` and console port `9001`.
- `minio-init`, a one-shot MinIO client container that creates the `tflive-artifacts` bucket.

Local env config points the existing S3 artifact adapter at MinIO:

- `ARTIFACT_STORE_KIND=s3`
- `S3_BUCKET=tflive-artifacts`
- `S3_REGION=us-east-1`
- `S3_ENDPOINT=http://localhost:9000`
- `S3_ACCESS_KEY_ID=tflive`
- `S3_SECRET_ACCESS_KEY=tflive-secret`
- `S3_FORCE_PATH_STYLE=true`

## Testing

- Backend service tests cover empty and populated list use cases.
- API tests cover stack/template list routes.
- Postgres tests cover tenant-scoped list queries.
- UI client tests cover list route calls.
- UI state tests cover selecting API-loaded stacks/templates and refreshing after create/register where practical.
- Full verification runs `go test ./...`, `npm test`, and `npm run build`.
