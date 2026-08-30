-- The settings a connector declares about itself.
--
-- Held as a JSON document rather than a table of fields, because it is authored
-- by the connector, replaced wholesale every time it registers, and only ever
-- read in full so the dashboard can render it. A table would buy the ability to
-- query one field, which nothing wants to do.
ALTER TABLE connectors ADD COLUMN settings_schema TEXT NOT NULL DEFAULT '[]';
