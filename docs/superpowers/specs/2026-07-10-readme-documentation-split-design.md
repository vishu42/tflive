# README Documentation Split Design

**Date:** 2026-07-10

## Goal

Turn the root README into a concise onboarding document while preserving the existing system design in one discoverable user-facing architecture document.

## README Scope

`README.md` will retain only:

- the project name, MVP warning, and a short product description;
- a compact architecture summary that includes the transactional workflow outbox;
- local dependency, API, worker, and UI startup commands;
- a short repository/package map;
- a documentation section linking to the detailed architecture document and relevant implementation design records.

The README will not duplicate detailed domain models, workflow state sequences, security guidance, persistence inventories, scaling guidance, or deferred design questions.

## Architecture Document Scope

Create `docs/architecture.md` as the canonical home for the detailed material currently embedded in the README. It will contain:

- goals and MVP non-goals;
- the full system architecture and component responsibilities;
- core domain boundaries and models;
- package ownership and dependency flow;
- template registration and template-run workflows;
- transactional outbox reliability semantics;
- Terraform backend, input, credential, and GitHub integration contracts;
- approvals, cancellation, logs, activity records, and persistence ownership;
- runner security, scaling, and deferred design topics.

Existing content will be moved and lightly edited for transitions and internal links rather than substantively redesigned. The transactional-outbox behavior must remain consistent with the implementation: template-run state and intent are atomic in Postgres, worker-hosted dispatch is at least once, and deterministic Temporal workflow IDs make repeated starts idempotent.

## Navigation and Source of Truth

The root README is the entry point; `docs/architecture.md` is the detailed product and system-design source of truth. README architecture text will be intentionally summarized and link directly to the detailed document. Historical implementation specs and plans under `docs/superpowers/` remain unchanged and are not substitutes for current user-facing documentation.

## Verification

- Every existing README section is either retained in concise form or represented in `docs/architecture.md`.
- Local startup commands remain in the README and are unchanged unless a broken reference is found.
- Relative Markdown links resolve to files in the repository.
- `git diff --check` reports no whitespace errors.
- The existing Go test suite remains green because documentation restructuring must not affect source code.
