-- A ref is a property of the revision, not of the install.
--
-- stack_templates.selected_ref was a copy of the chosen revision's source_ref,
-- taken at install time and never updated afterwards: changing the desired
-- revision left it pointing at whatever the component was first installed from.
-- Until the run fetch moved to resolved_commit_sha it also decided what a run
-- checked out, so the stale copy was executable. Now that runs check out the
-- revision's commit, the column has no reader that can be right — the desired
-- revision's source_ref is authoritative and, because revisions are immutable
-- per commit, cannot go stale.
--
-- last_applied_ref goes with it. It was denormalised from the run on every
-- apply and read by nothing; the applied revision is already recorded in
-- last_applied_template_revision_id, and the run keeps its own selected_ref.
--
-- template_runs.selected_ref is deliberately untouched. On a run a ref is a
-- fact about the past — provenance of what was started, frozen at start — not
-- a claim about the present that has to be maintained.
alter table stack_templates
	drop column if exists selected_ref,
	drop column if exists last_applied_ref;
