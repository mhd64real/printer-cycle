-- Sessions, so core knows who a person is rather than taking a connector's word
-- for it.
--
-- Until this existed, a connector approving an identity pairing simply named the
-- user it claimed was approving, and core had no way to check. Three separate
-- features were leaning on that: identity approval, jobs submitted on somebody's
-- behalf, and the dashboard's own login.
--
-- Only a hash of each token is stored. Core never needs the token itself again,
-- only to recognise one being presented, so keeping it would mean holding a
-- bearer credential for no reason. Same reasoning as connector enrolment.
CREATE TABLE user_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Which connector hosted the sign-in. A session is usable only by the
    -- connector that created it, so a session minted by the dashboard cannot be
    -- replayed by some other connector that happened to see it.
    connector_id TEXT REFERENCES connectors(id) ON DELETE CASCADE,

    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at   TEXT NOT NULL,
    last_used_at TEXT
);

CREATE INDEX idx_sessions_user   ON user_sessions(user_id);
CREATE INDEX idx_sessions_expiry ON user_sessions(expires_at);
