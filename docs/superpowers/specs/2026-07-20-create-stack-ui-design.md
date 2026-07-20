# Create Stack UI

**Date:** 2026-07-20
**Status:** Draft

## Overview

Build the `/stacks/new` page to allow users with `canCreateStack` capability to create a new stack. The route already exists in the router but renders a placeholder.

## Scope

- A single page form with one field: `name` (required)
- Slug is auto-derived by the backend (empty string passed)
- Tags default to `{}`, default_credential_ids default to `[]`
- On success: invalidate stacks list and navigate to `/stacks/{newStackId}`

## Design

### Component: `CreateStackScreen`

**Location:** `web/src/features/stacks/CreateStackScreen.tsx`

**Layout:**
- Wrapped in a `.panel` card, centered on page
- "Create stack" heading
- Back link to `/stacks`
- `name` text input (required, trimmed)
- Submit button with `Loader2` spinner when pending
- `.alert` error banner on mutation failure

**States:**
- Idle: empty form
- Validating: client-side — name must be non-empty after trim
- Submitting: button disabled, spinner shown
- Error: alert banner with error message, form remains editable
- Success: redirect to `/stacks/{id}`

**Data flow:**
- Uses `useCreateStackMutation(tenantID)` from `queries.ts`
- Calls `createStack(tenantID, { name, slug: '', tags: {}, default_credential_ids: [] })`
- On success: `queryClient.invalidateQueries({ queryKey: queryKeys.stacks(tenantID) })` then `navigate(`/stacks/${result.id}`)`
- Auth errors (401, 403) are handled by the RequireCapability route gate and OidcAuthProvider. Mutation errors are caught inline and displayed in an alert banner.

### Route change

**File:** `web/src/app/router.tsx`

Replace the `RoutePlaceholder` at `/stacks/new` with `CreateStackScreen`:

```diff
- { index: true, element: <RoutePlaceholder title="Create stack" /> }
+ { index: true, element: <CreateStackScreen /> }
```

## Conventions

- Follows existing CSS from `styles.css`: `.panel`, `.primary-button`, `.form-grid`, `.alert`, `.muted`, `.spin`
- Uses only existing dependencies: `lucide-react`, `react-router-dom`, `@tanstack/react-query`
- Matches patterns from `StacksListScreen.tsx` and `StackAccessScreen.tsx`
- No modals, no dialogs, no new dependencies

## Files

| File | Action |
|------|--------|
| `web/src/features/stacks/CreateStackScreen.tsx` | Create |
| `web/src/app/router.tsx` | Edit (swap placeholder for real component) |
