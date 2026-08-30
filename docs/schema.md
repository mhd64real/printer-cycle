# Database schema

SQLite, one file, applied by numbered migrations in `internal/store/migrations`.

## The shape of it

| table | holds |
| --- | --- |
| `meta` | singleton values belonging to the installation |
| `users` | people with accounts on this box |
| `printers` | queues printer-cycle created in CUPS |
| `printer_access` | who may use a printer, only meaningful when it is restricted |
| `connectors` | installed connectors, their credentials and their policy |
| `connector_scopes` | what each connector is permitted to do |
| `connector_settings` | values for the settings a connector declared |
| `identity_links` | bindings from an external identity to a user |
| `identity_link_requests` | pairing codes awaiting approval |
| `jobs` | printer-cycle's own record of everything printed |

## Decisions worth knowing

**Multi-user from the first migration.** A fresh install has one user and hides user management until
a second exists, but that is a decision about the interface, not about the data. Building a
single-user schema and retrofitting households later would mean a migration and a breaking change on
something other people's connectors depend on.

**A printer belongs to the box, not to a person.** A household printer is a shared appliance.
Per-user ownership is a SaaS instinct applied where it does not fit, so `printers.restricted`
defaults to 0 and `printer_access` only means anything once somebody turns restriction on.

**Job history outlives the connector that created it.** `jobs.connector_id` is set to null when a
connector is uninstalled rather than cascading, because somebody removing a Telegram bot should not
lose the record of everything they printed through it. Deleting a *printer* does cascade, since a job
with no printer is a row nothing can render.

**printer-cycle keeps its own job records.** Not duplication for its own sake: CUPS forgets completed
jobs after a while, so anything that has to outlive that, which includes a user's own history, has to
live here. `cups_job_id` links to whatever CUPS still remembers, and is unique only among the jobs
that have one, since many can be waiting to reach CUPS at once.

**Usernames are case insensitive.** Two accounts differing only in capitals would be
indistinguishable to the person trying to log in.

**Secret settings are never returned to any client**, the dashboard included. `connector_settings.is_secret`
marks them.

## What core stores to authenticate connectors

`connectors.auth_method` is `ed25519` and `credential` holds a **public key**.

Core never stores anything capable of impersonating a connector, so a copied or leaked database file
is worth nothing to an attacker. See `PROTOCOL.md` section 4.

`auth_method` exists so the scheme can change later without a migration. An earlier draft used
HMAC-SHA256 over a nonce, which works but requires core to hold each connector's secret in readable
form, making read access to this file equivalent to impersonating every connector on the box. That
was changed on 2026-08-31, before anything depended on it.
