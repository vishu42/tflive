# Run Read And Log API Design

## Goal

Expose enough run state and persisted phase logs for a UI or CLI to observe a `TemplateRun` after it has been started.

## Architecture

The API keeps using `app.Service` as its boundary. `internal/app` adds read use cases for fetching one tenant-owned run and reading a phase log. Postgres remains the source of truth for run metadata. Local phase log files remain the current log-body store for this slice.

`internal/logsink` owns phase log path safety for both writes and reads. The service depends on a small `TemplateRunLogReader` interface so future object storage can replace local file reads without changing API handlers.

## API

Add:

```text
GET /v1/tenants/{tenant_id}/template-runs/{run_id}
GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs/{phase}
```

The run endpoint returns the existing `traits.TemplateRun` JSON shape. The log endpoint returns `text/plain; charset=utf-8` and the contents of the requested phase log.

## Log Lookup

Local log files are located under the configured run root:

```text
<run_root>/<tenant_id>/<run_id>/logs/<phase>.log
```

The log reader rejects unsafe tenant IDs, run IDs, and phases. Missing log files return an app-level not-found error, which the API maps to HTTP 404.

## Error Handling

Invalid IDs or phases return HTTP 400. Missing tenant-owned runs or missing phase logs return HTTP 404. Unexpected store or filesystem errors return HTTP 500.

## Out Of Scope

This slice does not implement SSE, object storage, log metadata rows, redaction, retention, run lists, activity events, or template/stack creation APIs.

## Testing

Tests cover service read validation, Postgres tenant-scoped run lookup, local log reader path safety and missing-file behavior, and API response codes/content types for run and log reads.
