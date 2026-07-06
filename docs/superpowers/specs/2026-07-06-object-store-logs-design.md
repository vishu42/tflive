# Object Store Logs Design

## Goal

Move API-visible Terraform phase logs off shared local disk and into an object-store boundary that can use local filesystem storage for development and S3-compatible storage for deployment.

## Architecture

`internal/artifacts` owns object keys and object-store adapters. Terraform activities keep writing command output to a local phase file while the command runs, then upload the completed phase log to object storage. The API reads phase logs through the same artifact log reader instead of reading the worker run directory.

Local files remain a worker-private spool. They are no longer the API's durable log source when an object store is configured.

## Object Keys

Run log keys are tenant/run/phase scoped:

```text
tenants/{tenant_id}/runs/{run_id}/logs/{phase}.log
```

The key builder validates tenant IDs, run IDs, and phases as safe path components.

## Adapters

The first adapters are:

- `filesystem`: local development object-store substitute rooted at `ARTIFACT_STORE_FILESYSTEM_ROOT`.
- `s3`: standard-library S3-compatible `PUT`/`GET` adapter using AWS SigV4.

The app uses `ARTIFACT_STORE_KIND=filesystem|s3`. Production should use `s3`; local runs can use `filesystem` or any compatible local endpoint later.

## Error Handling

Missing objects map to `os.ErrNotExist`, which the app maps to not found. Upload failures fail the Terraform activity after the command exits, so the run does not look complete while its durable log artifact is missing.

## Out Of Scope

This slice does not add SSE, log metadata rows, multipart upload, IAM role credential discovery, object retention policies, encryption configuration, or Loki/OpenSearch mirroring.

## Testing

Tests cover object key construction, filesystem store get/put and traversal rejection, S3 request signing shape via `httptest`, activity log upload after command completion, and API/worker config wiring.
