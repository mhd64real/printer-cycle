# printer-cycle build plan

From nothing to a published, marketed GitHub project.

## How this plan works

Mohamed says `next`, one stage gets built, then this file is updated: stages get marked done, and
future stages get edited, split, added, or deleted based on what the work actually revealed. The
plan is expected to change. That is the point of writing it down rather than holding it in a head.

Stage status is one of `todo`, `doing`, `done`, `cut`, `blocked`.

## Working conditions (set 2026-08-30)

- **Hardware available: a Mac only.** No Pi, no printer. So Linux CUPS runs in Docker for the whole
  build, printing is verified against virtual queues, and every stage that needs real hardware is
  quarantined in Phase 9 rather than blocking anything.
- **The repo is public from the first commit.** Every stage must leave `main` in a state that is not
  embarrassing to a stranger. README says honestly what works and what does not, from day one.
- **Launch is the full version:** README and releases, a compatibility list, a landing page, a demo
  video, and launch posts.

## Frozen decisions (from the design session, see PROTOCOL.md)

CUPS is the engine, reached only by IPP over its socket. Core is Go, targeting a Pi Zero 2 W.
Core does login, register a printer, print, and nothing else. Zero connectors ship. The dashboard is
a connector with no backdoor. One WebSocket protocol, JSON-RPC over text frames, documents over
binary frames. Multi-user schema, single-user defaults. All drivers installed by default, with
ranked auto-selection. Install script, no SD image. No scanning. GPLv3.

**Frontend, decided here:** Vite + React + TypeScript + Tailwind v4 + shadcn/ui, built to static
files and embedded in the dashboard binary. Not Next.js: there is no server to render on, and the
output has to be a folder of files a Go binary serves.

---

# Phase 0: Repo foundations

### Stage 1: Initialise the repo
- `git init`, and set repo-local `user.name` and `user.email` to `mhd64.real@gmail.com` BEFORE the
  first commit.
- `.gitignore` for Go, node, build output, SQLite files.
- **Done when:** `git log` after the first commit shows the right author and committer.
- **Status:** done, 2026-08-30
- **Notes:** default branch `main`. The machine global identity was already correct; repo-local was
  set anyway so a clone on another machine inherits it. First commit also carried `PLAN.md` and
  `PROTOCOL.md`, which were already sitting in the folder, so Stage 4 shrank.

### Stage 2: Licence and Go module
- `LICENSE` with the full GPLv3 text.
- `go mod init github.com/mhd64real/printer-cycle`.
- **Done when:** `go build ./...` runs clean on an empty module.
- **Status:** done, 2026-08-30
- **Notes:** Go was not installed on the Mac; installed 1.27.0 via Homebrew. The `go` directive in
  `go.mod` was deliberately lowered from the generated `1.27.0` to `1.24`, so contributors on
  distro-packaged Go can build without chasing the newest toolchain. Nothing in the design needs
  anything newer.

### Stage 3: Honest starting README
- What it is, what it is for, and a status line saying plainly that it does not work yet.
- The two halves of the pitch: old printers modern devices cannot see, and printers whose software
  is bad.
- Explicit note that scanning is out of scope.
- **Done when:** a stranger landing on the repo understands the idea and is not misled about
  readiness.
- **Status:** done, 2026-08-30
- **Notes:** the connector licensing sentence went into the README here as well, since that is where
  a prospective connector author actually looks first. Stage 4 still owns putting it in the spec.

### Stage 4: Add the licensing note to the protocol spec
- `PROTOCOL.md` was committed in Stage 1, so only the licensing section is left to write.
- A short section stating that implementing this protocol creates no licensing obligation, so
  connector authors can licence their own repos however they like.
- **Done when:** the licensing sentence exists in the spec.
- **Status:** done, 2026-08-30
- **Notes:** placed immediately after the opening, before section 0, since that is the first thing a
  connector author reads. Went further than planned and also declared the protocol itself free for
  anyone to implement on either side, including in a competing implementation of core. That invites
  alternative cores, which is what separates a protocol from an API.

### Stage 5: Repo layout, compiling stubs, and Makefile
- `cmd/core`, `cmd/dashboard`, `internal/`, `web/`, `scripts/`.
- **Minimal `main.go` stubs in both `cmd/` directories that actually compile.** Added to this stage
  after Stage 2: with no Go packages at all, `go vet ./...` exits 1 ("no packages to vet"), which
  would make the Stage 6 CI red on an empty tree. The first real Go code is not until Stage 10, so
  the stubs have to come from here.
- Makefile targets for `linux/arm64`, `linux/amd64`, `linux/arm` (armv7), and host.
- **Done when:** `make build-all` produces binaries for all three Linux targets from the Mac, and
  both `go build ./...` and `go vet ./...` exit 0.
- **Status:** done, 2026-08-30
- **Notes:** all six binaries cross-compile from the Mac and `file` confirms each architecture,
  including ARM aarch64 for the Zero 2 W. `CGO_ENABLED=0` throughout, so everything is statically
  linked with no libc dependency on the target. `internal/version` holds a `Version` var stamped at
  link time by the Makefile, which Stage 75 will need at release. Empty `web/` and `scripts/` carry
  `.gitkeep` for now. Stub arm64 binary is 1.5MB.

### Stage 6: Push to GitHub, wire CI
- Create the public repo, push, set description and topics.
- GitHub Actions: build all targets and run `go vet` on every push.
- **Done when:** the badge is green on a public repo.
- **Status:** done, 2026-08-30
- **Notes:** live at github.com/mhd64real/printer-cycle. CI runs gofmt, vet, test, and the full
  cross-compile on every push and pull request, green in about 25 seconds. `actions/checkout` and
  `actions/setup-go` are pinned to v7: v4 and v5 still work but target Node 20, which runners have
  deprecated, and a warning on every run of a public repo is noise nobody needs. `go-version-file:
  go.mod` rather than a hardcoded version, so the Go floor lives in exactly one place.

---

# Phase 1: A dev environment with no hardware

### Stage 7: CUPS in Docker
- A container running Linux `cupsd` with every driver package installed, listening on 631.
- Admin enabled for a dev user so `CUPS-Add-Modify-Printer` works.
- **Done when:** `http://localhost:631` serves the CUPS web UI from the container.
- **Why TCP and not a socket:** IPP is the same protocol either way, only the transport differs, and
  TCP from the Mac into the container keeps the Go build native and the loop fast.
- **Status:** done, 2026-08-30
- **Notes:** `debian:trixie-slim`, built arm64 on Apple Silicon, which happens to match the Pi
  target. Image is 512MB and carries **17,974 PPDs**, so driver ranking in Phase 6 gets developed
  against a realistic catalogue rather than a toy one. Published on **127.0.0.1:6631**, not 631,
  because macOS runs its own cupsd on 631. Authentication is off entirely: production reaches cupsd
  over a Unix socket where peer credentials identify an `lpadmin` user and no password is ever sent,
  and no-auth-over-TCP reproduces that observable behaviour more faithfully than basic auth would.
  `FileDevice Yes` goes in `cups-files.conf`, not `cupsd.conf`, since CUPS 1.6. Make targets
  `dev-up`, `dev-down`, `dev-logs`, `dev-shell` added.

### Stage 8: Virtual printers, including one that can be discovered
- A script that creates two queues in the container: one PostScript, one that writes output to a
  file so job completion can be verified byte for byte.
- **Added after Stage 7, and the reason matters.** The container as built can discover nothing. The
  `dnssd` backend exits with "Unable to create Avahi client" because no avahi-daemon is running, and
  `usb` has nothing to enumerate. Without a fix, Stages 13, 14, 32 and 33 could be written but never
  actually verified. So this stage also has to provide something discoverable:
  - `avahi-daemon` running in the CUPS container so the `dnssd` backend works at all.
  - A second container running `ippeveprinter` (from `cups-ipp-utils`), advertising itself over
    DNS-SD as a real IPP Everywhere printer for `CUPS-Get-Devices` to find.
- **Done when:** a job submitted by hand reaches `done` and produces a file, AND `lpinfo -v` inside
  the container lists the virtual network printer.
- **Status:** done, 2026-08-30
- **Notes:** the virtual printer needed its own container, not just another process beside cupsd. The
  first attempt ran `ippeveprinter` next to CUPS and the dnssd backend logged "Ignoring local service
  Virtual Office Printer", because it deliberately skips what the local machine advertises. Split
  into a second container with its own network namespace, cross-container mDNS works and cupsd now
  sees it the way it would see real hardware, UUID and TXT record included.
- Queues are `file-ps` (Generic PostScript) and `file-pcl` (Generic PCL laser). The PCL one matters
  more: it runs the Ghostscript rasterisation chain, which is the path every old printer depends on.
  Verified by inspecting the output, which begins with the PCL reset sequence `ESC E`, so the chain
  genuinely ran rather than passing bytes through.
- Reproducible from nothing with `make dev-down && make dev-up && make dev-printers`.

### Evidence on the CUPS 3.0 driver risk (recorded here 2026-08-30)

The design session flagged that CUPS 3.0 intends to remove printer drivers, which threatens this
project's entire premise. Running `lpadmin` produced the warning first-hand:

```
lpadmin: Printer drivers are deprecated and will stop working in a future version of CUPS.
```

The concrete position, from the target platform rather than from recollection: **Debian trixie ships
CUPS 2.4.10 and cups-filters 1.28.17. Drivers are deprecated but fully working, and CUPS 3.0 is not
in the distribution.** Raspberry Pi OS follows Debian, so the same holds there. printer-cycle is
viable today and the risk is future tense, not present tense. Revisit when a distro this targets
actually ships CUPS 3.x.

### Stage 9: Document the dev loop
- `docs/development.md`: start the container, create the queues, run core, what to expect.
- **Done when:** the file is enough for someone else to get running without asking.
- **Status:** todo

---

# Phase 2: The IPP client (the foundation everything rests on)

### Stage 10: IPP transport
- `http.Client` with a `DialContext` that speaks either a Unix socket path or a TCP address, chosen
  by config. `Content-Type: application/ipp`.
- **Done when:** a raw request reaches cupsd in the container and a response comes back.
- **Status:** todo

### Stage 11: First real request, Get-Printers
- Build, encode, send, decode with `goipp`. Map into a Go `Printer` struct.
- **Done when:** the two virtual queues from Stage 8 come back as typed Go values.
- **Status:** todo

### Stage 12: Status codes to Go errors
- Map IPP status codes onto Go error values, and those onto the JSON-RPC codes in PROTOCOL.md
  section 3.
- **Done when:** asking for a printer that does not exist yields a typed not-found error, not a
  string.
- **Status:** todo

### Stage 13: Discovery, CUPS-Get-Devices
- Call it with a timeout, decode the device list, expose device URI, device ID, make and model, and
  transport.
- **Done when:** the container's backends return a device list into Go structs, including the
  virtual IPP printer from Stage 8.
- **Known gap, honest about it:** the `dnssd` path becomes testable via Stage 8's virtual printer,
  but **SNMP discovery cannot be tested without either real hardware or a simulated SNMP printer
  agent.** SNMP is the path that finds exactly the old network lasers this product exists for, so
  this is not a trivial gap. It stays unverified until Phase 9.
- **Status:** todo

### Stage 14: Streaming discovery
- Read the response progressively so devices surface as they are found instead of after the SNMP
  timeout expires.
- **Done when:** results arrive on a Go channel over several seconds rather than all at once at the
  end.
- **Status:** todo

### Stage 15: PPD listing with device-id filter
- `CUPS-Get-PPDs`, both the full list and filtered by `ppd-device-id`.
- **Done when:** a device ID string returns only its candidate PPDs.
- **Status:** todo

### Stage 16: Create and delete printers
- `CUPS-Add-Modify-Printer` and `CUPS-Delete-Printer`.
- **Done when:** a queue can be created and removed from Go, and it shows up in the CUPS web UI in
  between.
- **Status:** todo

### Stage 17: Print-Job with a streaming body
- IPP header first, then document bytes, through an `io.Pipe`, so nothing large is ever fully
  resident.
- **Done when:** a PDF prints to the file-backed queue and the output file matches expectations.
- **Status:** todo

### Stage 18: Job queries and cancel
- `Get-Job-Attributes`, `Get-Jobs`, `Cancel-Job`.
- **Done when:** a submitted job can be read back and cancelled mid-flight.
- **Status:** todo

### Stage 19: Subscriptions and the event loop
- `Create-Printer-Subscription`, then a `Get-Notifications` loop with lease renewal, emitting job
  state changes on a channel.
- **Done when:** printing a document produces state change events with no polling anywhere.
- **Status:** todo

### Stage 20: Measure idle cost, and write the fallback if needed
- Measure CPU with the subscription loop idle. If subscriptions prove unreliable, add internal
  polling behind the same channel interface so nothing above it changes.
- **Done when:** idle CPU is measured and recorded in `docs/`, and the event channel is dependable
  either way.
- **Status:** todo

---

# Phase 3: Storage and identity

### Stage 21: SQLite and migrations
- `modernc.org/sqlite`, never the cgo driver, so cross-compilation stays one command.
- This is now enforced mechanically rather than by discipline: the Makefile from Stage 5 builds with
  `CGO_ENABLED=0`, so a cgo driver fails `make build-all` immediately instead of silently producing
  a binary that only runs on the build host.
- A tiny embedded migration runner.
- **Done when:** a fresh database is created and migrated on first run.
- **Status:** todo

### Stage 22: Schema
- `users`, `printers`, `connectors`, `connector_scopes`, `connector_settings`, `identity_links`,
  `jobs`. Multi-user from the first migration, as decided.
- **Done when:** the schema is applied and documented in `docs/schema.md`.
- **Status:** todo

### Stage 23: Users
- Create, list, delete. Argon2id password hashing. First user is admin.
- **Done when:** users round-trip through the database and a wrong password is rejected.
- **Status:** todo

### Stage 24: Connector registry and tokens
- Install a connector record, generate its shared secret, store scopes.
- **Done when:** a connector can be registered and its secret issued once, never readable again.
- **Status:** todo

### Stage 25: HMAC nonce verification
- Per-connection nonce, `HMAC-SHA256(secret, nonce)` verification, constant-time comparison.
- **Done when:** a correct proof authenticates and a replayed one from a previous connection fails.
- **Status:** todo

### Stage 26: First-run bootstrap
- On an empty database, core prints a one-time setup token to stdout and writes it to a file with
  tight permissions. The dashboard uses it to register itself and create the first admin.
- **Done when:** a fresh install goes from empty to logged-in dashboard with no manual database work.
- **Status:** todo

---

# Phase 4: The protocol server

### Stage 27: WebSocket listener
- `/v1/connector` on TCP, and optionally on a Unix socket path, same handler for both.
- **Done when:** a `websocat` client connects to both and receives the `hello` notification.
- **Status:** todo

### Stage 28: JSON-RPC message layer
- Encode and decode, request and response correlation by id, notifications, both directions.
- **Done when:** core can call a connected client and receive its reply.
- **Status:** todo

### Stage 29: Handshake
- `hello` with nonce, `authenticate`, granted scopes returned.
- **Done when:** an unauthenticated client can call nothing but `authenticate`.
- **Status:** todo

### Stage 30: Registration and manifest
- `register`, manifest validation, settings schema stored, identity policy recorded.
- **Done when:** a connector's declared settings survive a core restart.
- **Status:** todo

### Stage 31: Scope enforcement
- One middleware, deny by default, correct JSON-RPC error on refusal.
- **Done when:** a connector holding only `jobs.submit` is refused `printers.manage` with code
  -32001.
- **Status:** todo

### Stage 32: printers.discover and printer.discovered
- Wire the streaming discovery from Stage 14 to progressive notifications.
- **Done when:** a client sees devices arrive one at a time.
- **Status:** todo

### Stage 33: printers.probe
- Resolve a bare address by trying 631, then 9100, then 515, plus SNMP for the model string.
- **Done when:** giving it the container's address returns a usable device URI and a model.
- **Status:** todo

### Stage 34: printers.add and printers.remove
- Pairing, with `ppd: null` meaning use the top-ranked candidate.
- **Done when:** a discovered device becomes a working queue in one call.
- **Status:** todo

### Stage 35: jobs.submit and binary streaming
- Allocate a stream id, accept binary frames, pipe them into Print-Job.
- **Done when:** a 50MB file prints without core's memory rising meaningfully.
- **Status:** todo

### Stage 36: jobs.commit, integrity, and timeouts
- Verify length and SHA-256, discard abandoned streams.
- **Done when:** a truncated upload is rejected with code -32007 and leaks nothing.
- **Status:** todo

### Stage 37: job.updated push
- Fan the Stage 19 event channel out to whichever connector owns each job.
- **Done when:** a connector receives state changes it never asked for, in order.
- **Status:** todo

### Stage 38: Identity linking
- `identity.linkRequest`, `identity.resolve`, the `identity.linked` notification, code expiry.
- **Done when:** a fake connector links an external id to a user through the full flow.
- **Status:** todo

### Stage 39: Settings read and change
- `settings.get`, the `settings.changed` notification, secret values write-only.
- **Done when:** editing a setting reaches a running connector without a restart, and secrets never
  come back out.
- **Status:** todo

### Stage 40: On-behalf-of enforcement
- A connector declaring `identity: none` cannot attribute jobs to a user; one declaring `linked`
  can, but only where a link exists.
- **Done when:** a forged `on_behalf_of` is rejected with -32006.
- **Status:** todo

---

# Phase 5: The dashboard connector

### Stage 41: Dashboard binary skeleton
- Connects to core as a connector, handles the handshake, serves static files, holds the token so
  the browser never sees it.
- **Done when:** it registers itself against a running core and serves a placeholder page.
- **Status:** todo

### Stage 42: Frontend scaffold
- Vite, React, TypeScript, Tailwind v4, shadcn/ui. Built output embedded with `go:embed`.
- **Done when:** `make build` produces one binary containing the whole frontend.
- **Status:** todo

### Stage 43: Browser to dashboard session layer
- Browser talks HTTP to the dashboard binary; the dashboard relays to core over the socket with its
  own credential, carrying the logged-in user.
- **Done when:** the browser never holds a connector token and cannot reach core directly.
- **Status:** todo

### Stage 44: First-run setup screen
- Consume the setup token, create the first admin, land logged in.
- **Done when:** a fresh install is usable from a browser with no terminal step after install.
- **Status:** todo

### Stage 45: Login and logout
- **Done when:** sessions survive a page reload and expire sensibly.
- **Status:** todo

### Stage 46: Printers page, discovery and pairing
- Progressive list, USB and LAN together, one pair button per device, the recommended driver named
  in plain words.
- **Done when:** a discovered virtual queue is paired in one click.
- **Status:** todo

### Stage 47: Manual add on the same page
- Type an IP, or paste a full URI for experts.
- **Done when:** an address alone produces a working queue.
- **Status:** todo

### Stage 48: Print page
- Choose printer, upload a file, set copies, duplex, colour, and media, submit.
- **Done when:** a PDF chosen in the browser prints to the file-backed queue.
- **Status:** todo

### Stage 49: Jobs page with live status
- Driven by pushes, never by polling. Cancel button.
- **Done when:** state changes appear without a refresh.
- **Status:** todo

### Stage 50: Connectors page and the generic settings renderer
- List installed connectors, enable and disable, and render any settings schema without knowing
  anything about the connector.
- **Done when:** a connector nobody anticipated gets a working settings page for free.
- **Status:** todo

### Stage 51: Identity link approval
- The screen where a pairing code is approved, plus the list of linked identities with revoke.
- **Done when:** a link can be approved and revoked from the browser.
- **Status:** todo

### Stage 52: Users page, hidden until needed
- Invisible while there is one user, appears when a second is added.
- **Done when:** a single-user install never sees user management, and a two-user install does.
- **Status:** todo

---

# Phase 6: Driver intelligence, the differentiator

### Stage 53: Parse IEEE 1284 device IDs
- Extract MFG, MDL, CMD, and friends into a struct.
- **Done when:** real device ID strings parse correctly, including malformed ones.
- **Status:** todo

### Stage 54: Candidate ranking
- The preference table as data, not code. Rank the candidates CUPS returns unranked.
- **Done when:** a device with several candidates gets a deterministic, sensible first choice.
- **Status:** todo

### Stage 55: Overrides and firmware flags
- The known-bad match list, and the flag for models needing non-redistributable firmware.
- **Done when:** an HP LaserJet 1018 is flagged as needing a firmware download before pairing, in
  plain language.
- **Status:** todo

### Stage 56: Firmware fetch flow
- Fetch at pair time, with a clear message when the box is offline. Never fail silently.
- **Done when:** the offline case explains itself instead of printing nothing.
- **Status:** todo

---

# Phase 7: The install script

### Stage 57: Distro and arch detection
- Detect the family and map package names for apt, dnf, pacman, apk, and zypper. Detect arm64,
  armv7, amd64.
- **Done when:** it reports correctly inside five distro containers.
- **Status:** todo

### Stage 58: Install CUPS and every driver
- Driver-only split packages, never the full vendor suites.
- Expect `lpadmin` to print a driver deprecation warning on every queue creation. It is noise, not a
  failure, on CUPS 2.4.x. The install script should not treat it as an error, and the dashboard
  should not surface it to users.
- **Done when:** a bare container ends with cupsd running and the driver set present.
- **Status:** todo

### Stage 59: Binaries, checksums, users, directories
- Download and verify, create the system user, add it to `lpadmin`, create config and data
  directories with correct permissions.
- **Done when:** core can perform a CUPS admin operation with no password anywhere.
- **Status:** todo

### Stage 60: Service units
- systemd, plus OpenRC for Alpine.
- **Done when:** both binaries start on boot and restart on failure.
- **Status:** todo

### Stage 61: Idempotency, minimal flag, uninstall
- Re-running upgrades rather than breaking. `--minimal` skips the big driver set. An uninstall script
  that actually removes everything.
- **Done when:** install, install again, uninstall, all clean, in every test container.
- **Status:** todo

---

# Phase 8: Connectors, which prove the whole design

### Stage 62: Example connector, deliberately tiny
- Under 150 lines, in its own directory, doing the handshake, declaring a setting, submitting a job.
- **Done when:** someone can read it in five minutes and copy it.
- **Status:** todo

### Stage 63: Connector author guide
- `docs/writing-a-connector.md`, built around the example.
- **Done when:** it answers the questions the example raises rather than restating the spec.
- **Status:** todo

### Stage 64: AirPrint connector, IPP server side
- Its own IPP endpoint that accepts jobs from phones and forwards them to core.
- **Done when:** a raw IPP client can print through it.
- **Status:** todo

### Stage 65: AirPrint connector, mDNS advertisement
- Advertise `_ipp._tcp` with the attributes iOS requires.
- **Done when:** an iPhone on the LAN lists the printer.
- **Status:** todo

### Stage 66: AirPrint end to end
- Phone to connector to core to CUPS to output file.
- **Done when:** printing from an iPhone produces the expected output.
- **Status:** todo

---

# Phase 9: Real hardware (quarantined, needs kit Mohamed does not have yet)

### Stage 67: Acquire hardware
- A Pi Zero 2 W or any spare Linux box, and at least one old printer, USB or network.
- **Status:** blocked, Mohamed's call on timing

### Stage 68: Real install on a real distro
- **Done when:** the install script works on hardware that was never a container.
- **Status:** blocked

### Stage 69: A real USB printer, paired automatically
- **Done when:** a physical printer is discovered, auto-matched to a driver, and prints in one click.
- **Status:** blocked

### Stage 70: Memory and CPU under real load on 512MB
- Confirm the serialized render queue holds and nothing gets OOM killed.
- **Done when:** a large colour PDF prints on a Zero 2 W without the kernel intervening.
- **Status:** blocked

---

# Phase 10: Documentation

### Stage 71: README rewrite
- The real pitch, screenshots, install one-liner, honest limitations including the x86 driver wall.
- **Status:** todo

### Stage 72: Compatibility list
- What works, what needs firmware, what will never work on ARM and why.
- **Status:** todo

### Stage 73: Architecture document
- Why CUPS, why connectors are processes, why the protocol is what it is.
- **Status:** todo

### Stage 74: Contributing, and the licence decision made deliberately
- Contribution terms, and a conscious choice about whether accepting outside code forecloses ever
  relicensing core.
- **Status:** todo

---

# Phase 11: Launch

### Stage 75: v0.1.0 release
- Tagged, with binaries for all three architectures and checksums.
- **Status:** todo

### Stage 76: Screenshots
- The pairing flow, the print page, live job status.
- **Status:** todo

### Stage 77: Landing page
- The pitch, screenshots, install one-liner, link to the repo.
- **Status:** todo

### Stage 78: Demo video
- A printer nobody could use, working from a phone, in under ninety seconds.
- **Status:** todo

### Stage 79: Launch posts
- Show HN, r/selfhosted, r/raspberry_pi. Written honestly, limitations included.
- **Status:** todo

### Stage 80: Post-launch triage
- Answer issues, log what people actually own, feed real device IDs back into the ranking table.
- **Status:** todo

---

# Revision log

Every change to this plan gets a line here, so the reasoning survives.

- **2026-08-30:** plan created. Written for a Mac-only build with Docker CUPS, a public repo from the
  first commit, and a full launch. Hardware stages quarantined in Phase 9 rather than allowed to
  block anything.
- **2026-08-30, after Stage 1:** Stage 4 reduced to just the licensing note, because `PLAN.md` and
  `PROTOCOL.md` already existed on disk and went into the first commit rather than waiting.
- **2026-08-30, after Stage 2:** Stage 5 gained compiling `main.go` stubs. Discovered while
  verifying Stage 2: `go vet ./...` exits non-zero when a module contains no packages, so the Stage 6
  CI would have gone red immediately on a repo that is public from commit one. Also recorded the
  deliberate `go 1.24` floor rather than the toolchain's `1.27.0`.
- **2026-08-30, after Stage 3:** no structural change. The README carries the connector licensing
  note in addition to Stage 4's copy in the spec, because that is the first place a connector author
  looks.
- **2026-08-30, after Stage 4:** no structural change. The licensing section also grants the right to
  implement the protocol in a competing core, which was not in the original scope of the stage but
  costs nothing and makes the spec a real protocol rather than a house API.
- **2026-08-30, after Stage 5:** no structural change; everything worked first time. Noted in Stage
  21 that `CGO_ENABLED=0` in the Makefile now enforces the pure-Go SQLite decision mechanically: a
  cgo driver will break the cross-compile loudly rather than quietly producing a host-only binary.
- **2026-08-30, after Stage 6:** Phase 0 complete. No structural change. Action versions bumped to
  v7 for the Node 20 deprecation, and CI reads the Go version from `go.mod` so it never drifts from
  the module. A CI badge went into the README.
- **2026-08-30, after Stage 7:** Stage 8 grew a second job. Running the backends by hand showed the
  container can discover nothing at all: `dnssd` cannot reach an Avahi client and `usb` has no
  devices, so four later stages would have been written blind. Stage 8 now also stands up
  avahi-daemon and an `ippeveprinter` virtual network printer. Logged against Stage 13 that SNMP
  discovery stays untestable until real hardware exists, since that is the path that finds the exact
  printers this product targets.
- **2026-08-30, after Stage 8:** the virtual printer had to move into its own container; the CUPS
  dnssd backend ignores locally advertised services, so the first arrangement would have made
  discovery look testable while testing nothing. Also resolved the CUPS 3.0 driver risk down to a
  fact rather than a worry: the target platform ships CUPS 2.4.10 with drivers deprecated but
  working. Noted against Stage 58 that the deprecation warning is expected output, not a failure.
