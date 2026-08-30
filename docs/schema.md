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

## An open question: what core stores to authenticate connectors

`connectors.auth_method` and `connectors.credential` are shaped to allow the scheme to change without
a migration, because there is a real choice here that has not been settled.

`PROTOCOL.md` section 4 specifies **HMAC-SHA256 over a per-connection nonce**. The connector never
transmits its secret, so a listener on the network learns nothing reusable. That property is real and
it is why plaintext on a home LAN is acceptable.

But verifying an HMAC requires holding the secret. Core cannot store a hash of it. So `credential`
holds the shared secret in a form core can read back, and the only thing protecting it is file
permissions on the database. **Anyone who can read the database file can impersonate every connector
on the box.**

**Ed25519 would remove that.** The connector generates a keypair at install, gives core the public
key, and signs the nonce. Core stores only public keys, so a copied database is worth nothing to an
attacker. The exchange is the same shape, the same number of round trips, and every language worth
writing a connector in has an Ed25519 library. `PROTOCOL.md` already sends `auth` as a list, so
adding it is additive rather than breaking, and section 4 is still marked PROPOSED.

This is recorded rather than decided, because it changes a security decision in the protocol and that
is not a call to make quietly. The schema supports either.
