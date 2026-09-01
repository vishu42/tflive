-- A session is tflive's own, not the IdP's.
--
-- Before this table the session cookie held the raw ID token, so how long a
-- sign-in lasted was decided by the provider's token lifespan and whether
-- renewal was silent by its SSO idle timeout. tflive is BYO-IdP and sets
-- neither on a customer's provider. A row here is a session we issue, expire,
-- and revoke on our own terms.
--
-- id_hash, not id: the value handed to the browser is 32 bytes of CSPRNG
-- output and only its SHA-256 reaches the database, so a dump of this table
-- yields no usable cookies.
--
-- No tenant_id. A session is an authentication fact about a browser and the
-- deployment serves one configured tenant; scoping it would imply a
-- cross-tenant question that cannot be asked here.
create table sessions (
	id_hash text primary key,
	subject text not null,
	name text not null,
	preferred_username text not null,
	email text not null,
	-- The ID token's sid claim. Empty when the provider omits it, in which
	-- case back-channel logout falls back to matching on subject.
	idp_session_id text not null,
	-- Encrypted at rest. Kept only for id_token_hint at RP-initiated logout,
	-- without which Keycloak shows a confirmation page instead of signing out.
	id_token_ciphertext text not null,
	created_at timestamptz not null,
	last_seen_at timestamptz not null,
	absolute_expires_at timestamptz not null,
	revoked_at timestamptz
);

-- Back-channel logout arrives with a sid or a sub and must find the live
-- sessions for it. Partial: revoked rows are never the target of a revoke.
create index sessions_idp_session_id_idx on sessions (idp_session_id)
where revoked_at is null;

create index sessions_subject_idx on sessions (subject)
where revoked_at is null;

-- Supports deleting rows that can no longer authenticate anyone.
create index sessions_absolute_expires_at_idx on sessions (absolute_expires_at);
