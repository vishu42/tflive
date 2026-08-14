-- config_json held two meanings: the config captured at install, and a fallback
-- for desired_config_json. The fallback has been dead since migration 0006 gave
-- desired_config_json `not null default '{}'` and backfilled every existing row
-- — a row read from this table can never have an empty desired config, so the
-- fallback branch is unreachable for anything that came from Postgres.
--
-- What is left is install-time history, so name it that. One meaning per column
-- is what stops the next reader from re-deriving desired state from the wrong
-- one, which is how the same four-line fallback ended up copied into three
-- different call sites.
alter table stack_templates
	rename column config_json to installed_config_json;
