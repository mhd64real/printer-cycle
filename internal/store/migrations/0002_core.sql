-- printer-cycle's core schema.
--
-- Multi-user from the first migration, deliberately. A fresh install has one
-- user and hides user management until a second exists, but that is a decision
-- about the interface, not about the data. Building a single-user schema and
-- retrofitting households later would mean a migration and a breaking change on
-- something other people's connectors depend on.

-- ---------------------------------------------------------------- users

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ------------------------------------------------------------- printers

CREATE TABLE printers (
    id           TEXT PRIMARY KEY,

    -- The CUPS queue name: sanitised, no spaces, no slashes. Derived from
    -- display_name, which is what the user actually typed and actually sees.
    queue_name   TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,

    device_uri   TEXT NOT NULL,
    ppd_name     TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',

    -- A printer belongs to the box, not to a person: a household printer is a
    -- shared appliance, and per-user ownership would be a SaaS instinct applied
    -- where it does not fit. Restriction is opt-in, and only then does
    -- printer_access mean anything.
    restricted   INTEGER NOT NULL DEFAULT 0 CHECK (restricted IN (0, 1)),

    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    created_by   TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE printer_access (
    printer_id TEXT NOT NULL REFERENCES printers(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    PRIMARY KEY (printer_id, user_id)
);

-- ----------------------------------------------------------- connectors

CREATE TABLE connectors (
    -- The identifier a connector authenticates as, for example "telegram-bot".
    id          TEXT PRIMARY KEY,

    name        TEXT NOT NULL,
    version     TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',

    -- How this connector identifies people:
    --   none    it does not know who anyone is, and every job it submits is
    --           attributed to fallback_user_id. This is the AirPrint case.
    --   linked  it resolves an external identity to a user before submitting.
    identity_policy  TEXT NOT NULL DEFAULT 'none'
                     CHECK (identity_policy IN ('none', 'linked')),
    fallback_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,

    -- How this connector proves who it is.
    --
    -- auth_method names the scheme so it can change without a migration, and
    -- credential holds whatever that scheme needs. For hmac-sha256 that is a
    -- shared secret, which core must be able to read back in full, because
    -- verifying an HMAC requires holding the secret. Hashing it is not an
    -- option, so the protection is file permissions on the database.
    --
    -- The column is deliberately shaped to allow a scheme where core stores
    -- only a public key and never holds anything that could impersonate a
    -- connector. See docs/schema.md.
    auth_method TEXT NOT NULL DEFAULT 'hmac-sha256',
    credential  TEXT NOT NULL,

    enabled     INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT
);

CREATE TABLE connector_scopes (
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    scope        TEXT NOT NULL,
    PRIMARY KEY (connector_id, scope)
);

CREATE TABLE connector_settings (
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,

    -- Secret values are never returned to any client, including the dashboard.
    is_secret    INTEGER NOT NULL DEFAULT 0 CHECK (is_secret IN (0, 1)),

    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (connector_id, key)
);

-- ------------------------------------------------------------ identity

-- Bindings between an external identity and a printer-cycle user.
--
-- Every connector login flow anyone will write ends here: Telegram chat
-- pairing, a portal with a code, a QR scan. Core owns the binding so there is
-- one place that answers "what is linked to my account, and how do I revoke
-- it", instead of one user table per connector.
CREATE TABLE identity_links (
    id           TEXT PRIMARY KEY,
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    display      TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (connector_id, external_id)
);

-- Pairing codes awaiting approval. Short-lived by design.
CREATE TABLE identity_link_requests (
    code         TEXT PRIMARY KEY,
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL,
    display      TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_link_requests_expiry ON identity_link_requests(expires_at);

-- ----------------------------------------------------------------- jobs

-- printer-cycle keeps its own record of every job.
--
-- Not duplication for its own sake: CUPS forgets completed jobs after a while,
-- so anything that has to outlive that, which includes a user's own history,
-- has to live here. cups_job_id is the link to whatever CUPS still remembers.
CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    cups_job_id  INTEGER,

    printer_id   TEXT NOT NULL REFERENCES printers(id)   ON DELETE CASCADE,

    -- Who the job is for. Set from the connector's identity link, or from the
    -- connector's fallback user when it does not identify people.
    user_id      TEXT REFERENCES users(id) ON DELETE SET NULL,

    -- Where it came from. Null once a connector has been uninstalled; the job
    -- history should outlive the connector that created it.
    connector_id TEXT REFERENCES connectors(id) ON DELETE SET NULL,

    name         TEXT NOT NULL DEFAULT '',
    document_format TEXT NOT NULL DEFAULT '',

    state         TEXT NOT NULL DEFAULT 'pending',
    state_reasons TEXT NOT NULL DEFAULT '',

    size_bytes   INTEGER NOT NULL DEFAULT 0,
    pages_total  INTEGER NOT NULL DEFAULT 0,
    pages_done   INTEGER NOT NULL DEFAULT 0,

    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT
);

CREATE INDEX idx_jobs_user    ON jobs(user_id, created_at DESC);
CREATE INDEX idx_jobs_printer ON jobs(printer_id, created_at DESC);
CREATE UNIQUE INDEX idx_jobs_cups_id ON jobs(cups_job_id) WHERE cups_job_id IS NOT NULL;
