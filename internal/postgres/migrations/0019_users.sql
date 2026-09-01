-- A projection of what a verified token asserted, not a directory.
--
-- Before this table, grant display names and the grant search box were served
-- by the Keycloak Admin API behind a service account holding query-users and
-- view-users on the realm. That coupled tflive to Keycloak specifically and put
-- the customer's IdP on the critical path for rendering a grants list. OIDC has
-- no standard directory-search endpoint to replace it with: /userinfo only ever
-- describes the bearer of the token presented, so it cannot look up a third
-- user, and every vendor alternative is an admin API needing elevated
-- permissions a customer security team must approve.
--
-- Every ID token tflive verifies already carries sub, email, and a display
-- claim. A row here is those three, written at sign-in. tflive is not the
-- source of truth for identity and does no account lifecycle: nothing creates,
-- disables, or deletes a row from outside a sign-in.
--
-- The consequence is stated rather than engineered around: a user who has never
-- signed in is not here and cannot be granted a role. They sign in once and are
-- grantable from then on. SCIM is the answer if pre-provisioning is ever
-- required, and is deliberately not this.
--
-- No tenant_id, for the same reason sessions has none. A sign-in is an
-- authentication fact and the deployment serves one configured tenant, so
-- scoping would imply a cross-tenant question that cannot be asked here.
create table users (
	-- The OIDC sub claim, and the same string that becomes user:<sub> in an
	-- OpenFGA tuple. Names and email addresses are display data; this is the
	-- only stable key.
	sub text primary key,
	-- Both may be empty: a provider is not required to send either claim, and
	-- display_name falls back through preferred_username and email to sub
	-- before it reaches this table.
	email text not null,
	display_name text not null,
	-- Set once, on the row's first sign-in, and never touched again. The upsert
	-- moves last_seen_at only.
	first_seen_at timestamptz not null,
	last_seen_at timestamptz not null
);

-- Deliberately no index for the search box.
--
-- The primary key already serves the hot path -- one `where sub = any($1)` per
-- grants list -- which is the read that used to be an N+1 of admin API calls.
-- Search is an infix ILIKE, which a b-tree on lower(column) cannot answer; only
-- a trigram index could, and pg_trgm means CREATE EXTENSION, a privilege the
-- application role is not guaranteed to hold in a customer's database. This
-- table holds one row per human who has signed in to this one deployment and
-- every search is bounded to at most 50 rows, so a sequential scan is the right
-- answer until a real deployment says otherwise. Adding the extension later is
-- a one-line migration.
