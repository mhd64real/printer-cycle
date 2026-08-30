-- Single-use tokens that let a brand new connector register its public key.
--
-- A connector authenticates by signing a nonce with its private key, but on
-- first run it has no key core knows about yet. The admin adds the connector in
-- the dashboard, which issues one of these; the connector generates a keypair,
-- presents the token together with its public key, and core records the key.
--
-- Only a hash of the token is stored. Core never needs the token itself again,
-- only to recognise one being presented, so keeping it would be storing a bearer
-- credential for no reason. That is the same reasoning that made connector
-- authentication Ed25519: hold the least that still works.
CREATE TABLE connector_enrolments (
    token_hash   TEXT PRIMARY KEY,
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    expires_at   TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),

    -- Kept rather than deleted on use, so a token presented twice can be
    -- recognised as a replay in the log rather than looking like a token that
    -- never existed.
    used_at      TEXT
);

CREATE INDEX idx_enrolments_connector ON connector_enrolments(connector_id);
