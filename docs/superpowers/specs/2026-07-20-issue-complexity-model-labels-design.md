# Issue Complexity → Model Size Labels

**Date:** 2026-07-20
**Status:** Approved (rubric approved by user, classification table pending user review)

## Purpose

Triage all open GitHub issues with a label indicating the approximate model size
(parameter count) appropriate to solve them, so issues can be routed to
cost-efficient models: small/cheap models for trivial issues, large models only
where reasoning depth justifies the token spend.

## Labels

Six additive labels, namespaced so they group in the GitHub UI:

`model:250b`, `model:500b`, `model:750b`, `model:1t`, `model:1.25t`, `model:1.5t`

## Classification Rubric

Classification is based on **reasoning depth + blast radius**, not diff size.
A one-line fix requiring understanding of Temporal replay semantics ranks higher
than a 10-file mechanical refactor.

| Label | Tier | Criteria |
|---|---|---|
| `model:250b` | Trivial | Mechanical, single file/config, fix obvious from issue text, no judgment calls. |
| `model:500b` | Simple | One component, clear fix path, add/adjust a test. |
| `model:750b` | Moderate | Multi-file within one subsystem, needs component internals knowledge, some design judgment, tests required. |
| `model:1t` | Complex | Cross-component, subtle semantics (Temporal replay, TOCTOU, concurrency), or security-sensitive design choices. |
| `model:1.25t` | Hard | Cross-stack or architectural, ambiguous requirements needing design decisions. |
| `model:1.5t` | Epic | Multi-part feature / release gate spanning subsystems; realistically needs decomposition first. |

## Workflow

1. Fetch all open issue bodies via `gh issue list --json number,title,body,labels`.
2. Classify each issue per the rubric with a one-line justification.
3. Present the full classification table to the user for review/adjustment.
4. On approval: create the 6 labels (`gh label create`, green→red color gradient
   by size) and apply them (`gh issue edit --add-label`).
5. Labels are additive — existing labels are left untouched.

## Non-goals

- No automation/script for future issues (one-shot analysis; may be revisited).
- No modification of issue titles, bodies, milestones, or existing labels.
- No closing, deduplication, or reprioritization of issues.
