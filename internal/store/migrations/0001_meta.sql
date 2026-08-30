-- Singleton values that belong to the installation rather than to any user:
-- the install identifier, the first-run setup state, the schema's own notion of
-- when it was created.
--
-- A key/value table rather than a one-row table with a column per value, so
-- adding a setting later does not need a migration.
CREATE TABLE meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO meta (key, value) VALUES ('created_at', datetime('now'));
