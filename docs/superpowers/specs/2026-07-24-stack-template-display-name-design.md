# StackTemplate Display Name

## Problem

`stackTemplateLabel()` renders `meg_prod_a4de3e48 @ main (active)` — the workspace name is machine-generated and opaque. Users can't identify which template this is at a glance.

## Design

Derive a human-readable `display_name` from the linked `TemplateRevision`'s `repo_name` and `root_path`:

| root_path | display_name |
|-----------|-------------|
| `.` | `repo_name` (e.g. `infra-templates`) |
| `modules/vpc` | `repo_name/root_path` (e.g. `infra-templates/modules/vpc`) |

### Backend

1. **`traits.StackTemplate`** gains a transient `DisplayName` field (not persisted, populated by service layer).
2. **`Service.GetStack`** calls `ListTemplateRevisions` once after fetching templates, builds an ID→revision map, and sets `DisplayName` on each template.
3. **`stackTemplateResponse`** gains `display_name`, set from the trait in `newStackTemplateResponse`.

### Frontend

4. **`StackTemplate` type** gains `display_name: string`.
5. **`stackTemplateLabel()`** uses `display_name` instead of `workspace_name`. Falls back to `workspace_name` if display_name is empty.

### Rationale

One batch `ListTemplateRevisions` call (O(1) additional DB query) vs N+1 individual lookups. Resolved in the service layer (business logic) not the API layer (serialization).
