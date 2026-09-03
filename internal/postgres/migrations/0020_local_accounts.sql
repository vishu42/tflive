-- Accounts tflive authenticates itself, so a POC, demo, or test needs no IdP.
--
-- This is not a second identity provider. tflive signs nothing and issues no
-- token: a local sign-in verifies a password and then mints the same session
-- row the OIDC callback mints, and every request after it is authenticated by
-- the same opaque session cookie. See #211 and
-- docs/superpowers/specs/2026-09-01-local-accounts-considerations.md for why
-- the token-issuing design this replaced could not work.
--
-- Distinct from users (0019), which is a projection written at every sign-in
-- from whatever the provider asserted. This table is the credential store for
-- the accounts tflive owns, and a row here still projects into users at
-- sign-in through the ordinary path, so grants and search treat a local user
-- exactly like a federated one.
create table local_accounts (
	-- The same string that becomes user:<sub> in an OpenFGA tuple, and the sub
	-- of the projected users row.
	--
	-- It must not contain ':', '#', or '*': safeTupleToken
	-- (internal/authz/authorization.go) refuses those, so a natural-looking
	-- "local:root" would be accepted here and then rejected as an OpenFGA
	-- subject, leaving an account that can sign in and be granted nothing.
	-- Use local_root or a UUID. The constraint is stated here as well as in Go
	-- because the seeding path and any future CLI both write this column.
	sub text primary key
		constraint local_accounts_sub_is_a_tuple_token
		check (sub <> '' and sub !~ '[:#*[:space:][:cntrl:]]'),
	-- Stored case-folded. The login form matches on this column, so folding on
	-- write is what makes the unique constraint and the lookup agree about
	-- what a duplicate is -- "Root" and "root" are one account, not two.
	username text unique not null
		constraint local_accounts_username_is_folded
		check (username <> '' and username = lower(username)),
	-- PHC-encoded argon2id, parameters included, so raising the cost later
	-- verifies rows written under the old one without a migration.
	password_hash text not null,
	-- Projected into users at sign-in. Without them a local sign-in writes a
	-- blank-named row and the grants list has nothing to render.
	display_name text not null,
	email text not null,
	created_at timestamptz not null,
	updated_at timestamptz not null
);
