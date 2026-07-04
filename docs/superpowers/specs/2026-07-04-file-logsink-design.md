# File Logsink Design

## Goal

Implement the first `internal/logsink` slice by persisting Terraform command output to local files.

## Architecture

`internal/logsink` owns filesystem log creation. It creates a private `logs` directory under the prepared run workspace and opens append-only `<phase>.log` files. The worker activity keeps deriving the workspace path through `PrepareWorkspace`, then opens the correct phase log for each `RunTerraform` activity and passes that writer into the Terraform runner.

The runner stays responsible for process execution only. It accepts optional stdout and stderr writers on `TerraformCommand`; when they are omitted, it keeps the current stdout/stderr behavior.

## Data Flow

Terraform stdout and stderr are written to the same phase file for now:

```text
RunTerraform activity
  -> logsink.FileSink.OpenPhase("plan")
  -> runner.LocalProcessRunner
  -> <workspace>/logs/plan.log
```

`select_workspace` maps to `workspace.log`. `init`, `plan`, and `apply` map to their same-named phase files.

## Error Handling

The file sink rejects empty workspace paths, empty phases, and unsafe path components such as `../plan`. It creates log files with private permissions and returns wrapped errors to the activity. The activity closes the file after the Terraform command and reports close errors if the command itself succeeded.

## Out Of Scope

This slice does not implement redaction, live streaming, object storage, log metadata rows, retention, or separate stdout/stderr files.

## Testing

Tests cover file creation, append behavior, unsafe phase rejection, Terraform command-to-phase mapping, runner output writer plumbing, and local activity integration that writes Terraform output to the expected phase file.
