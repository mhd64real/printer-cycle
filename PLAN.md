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
- **Status:** done, 2026-08-30
- **Notes:** verified by tearing the environment down and following the document verbatim, rather
  than by rereading it. That caught one real gap: mDNS advertisement is not instant, so the discovery
  check run immediately after `make dev-up` returns nothing and reads as broken. The document now
  says so. Also covers the three things that would otherwise waste someone's afternoon: port 6631
  instead of 631, no authentication in the container and why that is the faithful choice, and the
  driver deprecation warning being expected output.

---

# Phase 2: The IPP client (the foundation everything rests on)

### Stage 10: IPP transport
- `http.Client` with a `DialContext` that speaks either a Unix socket path or a TCP address, chosen
  by config. `Content-Type: application/ipp`.
- **Done when:** a raw request reaches cupsd in the container and a response comes back.
- **Status:** done, 2026-08-30
- **Notes:** `internal/ipp`. One `Do` method serves both transports: `unix:///run/cups/cups.sock`
  for production and `http://127.0.0.1:6631` for the container, chosen by endpoint scheme, so nothing
  above this layer knows or cares which is in use.
- `Do` takes an optional body reader streamed straight after the encoded message. The combined length
  is unknown, so Go sends the request chunked, which is what will let Stage 17 and Stage 35 push a
  large document through without holding it in memory.
- `NewRequest` centralises the two attributes RFC 8011 requires first in every operation group, and
  hands out non-zero incrementing request ids. A test locks both down, because CUPS is lenient about
  the ordering and other IPP servers are not.
- Integration tests are gated behind `PRINTER_CYCLE_TEST_CUPS`, so `make test` and CI skip them and
  stay green with no container. `make test-integration` runs them against the dev environment.
- **Proof it works:** the round trip returned `client-error-not-found`, since the container has no
  default printer. That is the right result: an IPP-level answer means the message was encoded, sent,
  parsed by cupsd, answered, and decoded, with matching request ids. A transport failure would have
  looked nothing like it.

### Stage 11: First real request, Get-Printers
- Build, encode, send, decode with `goipp`. Map into a Go `Printer` struct.
- **Done when:** the two virtual queues from Stage 8 come back as typed Go values.
- **Status:** done, 2026-08-30
- **Notes:** both queues come back fully typed:

```
file-ps   state=idle  model="Generic PostScript Printer"  device=file:///var/spool/pc-out/file-ps.out
file-pcl  state=idle  model="Generic PCL Laser Printer"   device=file:///var/spool/pc-out/file-pcl.out
```

- **`resp.Groups` has to be walked directly.** goipp also exposes named per-group fields on Message,
  but a multi-printer response carries one Printer group per queue and those fields flatten them all
  together. Using them would have silently produced one merged printer out of many.
- The attribute helpers are forgiving on purpose: a missing attribute yields the zero value, never an
  error. Old printers omit a great deal, and failing on every omission would break the software on
  exactly the hardware it exists for.
- Marker levels follow the design decision that unknown is a legitimate answer. A negative level means
  the printer never reported one, which must not be confused with zero, since zero means empty. Test
  covers ragged marker arrays, where a printer returns fewer levels than names.
- Requests name the attributes they want rather than taking everything, keeping responses small on a
  box with a dozen queues.
- The status handling here is a placeholder, deliberately. Stage 12 replaces it with typed errors.

### Stage 12: Status codes to Go errors
- Map IPP status codes onto Go error values, and those onto the JSON-RPC codes in PROTOCOL.md
  section 3.
- **Done when:** asking for a printer that does not exist yields a typed not-found error, not a
  string.
- **Status:** done, 2026-08-30
- **Proof:**

```
typed error: ipp: Get-Printer-Attributes: client-error-not-found: The printer or class does not exist.
```

  and it satisfies `errors.Is(err, ipp.ErrNotFound)`, so callers branch on meaning rather than on
  message text.
- Two distinct kinds of failure are now distinguishable, which they were not before. A transport
  failure means the exchange never happened. An `*ipp.Error` means it worked perfectly and CUPS said
  no. Retry logic and user-facing messages need opposite things from those two.
- Success is a **range test**, not a list: RFC 8011 puts every code below 0x0100 in the successful
  family, and several of them are successes with a caveat (attributes ignored, values substituted).
  Treating those as failures would reject perfectly good print jobs for cosmetic reasons.
- CUPS's `status-message` is captured, because it is consistently more specific than the status code.
- Also added `Printer(ctx, name)` for single-queue lookup, which is what makes the missing-printer
  case testable at all.
- **The JSON-RPC mapping was deliberately NOT done here.** See the Stage 31 note for why.

### Stage 13: Discovery, CUPS-Get-Devices
- Call it with a timeout, decode the device list, expose device URI, device ID, make and model, and
  transport.
- **Done when:** the container's backends return a device list into Go structs, including the
  virtual IPP printer from Stage 8.
- **Known gap, honest about it:** the `dnssd` path becomes testable via Stage 8's virtual printer,
  but **SNMP discovery cannot be tested without either real hardware or a simulated SNMP printer
  agent.** SNMP is the path that finds exactly the old network lasers this product exists for, so
  this is not a trivial gap. It stays unverified until Phase 9.
- **Status:** done, 2026-08-30
- **Proof:** discovery returns exactly the three real devices, every backend pseudo-entry filtered:

```
ipps   class=network  uri=ipps://Virtual%20Office%20Printer._ipps._tcp.local/
dnssd  class=network  uri=dnssd://Virtual%20Office%20Printer._ipp._tcp.local/?uuid=e2e12bda-...
dnssd  class=network  uri=dnssd://Virtual%20Office%20Printer._printer._tcp.local/
```

- **A real bug, caught by a test before it shipped.** The first implementation used `net/url` to pull
  scheme and host out of a device URI. `url.Parse` rejects percent-encoding in the host, and CUPS
  puts the DNS-SD service name in the host position, so `dnssd://Virtual%20Office%20Printer._ipp...`
  parsed to an empty host and was discarded. **Every printer whose name contains a space would have
  vanished from discovery, which is most printers.** Replaced with plain scheme splitting.
- **CUPS mixes pseudo-devices in with real ones.** Each backend advertises itself as a device whose
  URI is a bare scheme (`beh`, `ipp`, `lpd`, `socket`), carrying `class=network` exactly like a real
  network printer, so class cannot separate them and the URI is the only discriminator. Unfiltered,
  the pairing screen would have offered seven things to pair with that are not printers.
- **`Unknown` is normalised to empty.** CUPS sends that literal string when a backend cannot identify
  hardware, and rendering it under a heading that says Model reads as a manufacturer name.
- **PROTOCOL.md corrected.** Section 7b listed `snmp` as a `transport` value. There is no `snmp://`
  scheme: transport says how to talk to a device, not how it was found, and SNMP-discovered printers
  arrive as `socket` or `lpd`. CUPS exposes no attribute naming the discovering backend, so the field
  as specified was fiction.

### Stage 14: Streaming discovery
- Read the response progressively so devices surface as they are found instead of after the SNMP
  timeout expires.
- **Done when:** results arrive on a Go channel over several seconds rather than all at once at the
  end.
- **Status:** done, 2026-08-30

**Measured first, before writing anything.** A throwaway experiment confirmed cupsd really does
stream `CUPS-Get-Devices`, chunked, rather than buffering it:

```
headers at 30ms, transfer-encoding=chunked, content-length=-1
t=30ms    1401 bytes   (the fast backends, all at once)
t=1.55s    286 bytes
t=2.06s    242 bytes
t=2.56s    200 bytes   then EOF
```

Had it turned out otherwise, this stage would have been impossible and the plan would have had to
change instead. Worth doing, since the whole stage rested on that assumption.

- **goipp has no incremental decoder**, and hand-writing an IPP parser would mean reimplementing
  attribute decoding including collections. Solved with the wire format instead: an IPP message is a
  header, then attribute groups, then a single end-of-attributes byte, so **any prefix plus that byte
  is a valid shorter message**. Appending it to whatever has arrived yields every group so far.
  Discovery responses are a couple of kilobytes, so re-decoding on each read costs nothing. A read
  landing mid-attribute simply fails to decode and the next read fixes it.
- **The last group is always withheld.** A group is only known complete once the next one begins, so
  emitting it early would deliver the same device twice with different contents. **Consequence worth
  remembering at Stage 46:** a device appears to the user when the *next* device is found, and the
  final one only when discovery ends. That is inherent, not a defect, but the pairing screen should
  not imply the list is settled before the call returns.
- **Deviation from the plan, deliberate: a callback, not a channel.** `DiscoverDevices(ctx, timeout,
  func(Device))`. A channel needs either an arbitrary buffer or a goroutine the caller must fully
  drain or leak, and it needs a second channel for the error. A callback has no lifetime question,
  returns errors normally, and maps one-to-one onto the `printer.discovered` notification Stage 32
  sends. `Devices` is now a thin collector over it, so there is one implementation rather than two.
- `Do` was split into `send` plus decode, so one caller can read the body progressively while every
  other keeps the simple path.

### Stage 15: PPD listing with device-id filter
- `CUPS-Get-PPDs`, both the full list and filtered by `ppd-device-id`.
- **Done when:** a device ID string returns only its candidate PPDs.
- **Status:** done, 2026-08-30

**The core promise of this product, demonstrated.** 17,974 drivers narrowed to two, from nothing but
what the hardware says about itself:

```
MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;

foo2zjs:0/ppd/foo2zjs/HP-LaserJet_1018.ppd   HP LaserJet 1018 Foomatic/foo2zjs-z1 (recommended)
drv:///hpcups.drv/hp-laserjet_1018.ppd       HP LaserJet 1018, hpcups 3.22.10, requires proprietary plugin
```

- **Correction to a design assumption: CUPS does give a ranking hint.** The design session concluded
  CUPS returns candidates unranked. Not quite: foomatic PPDs carry `(recommended)` inside
  `ppd-make-and-model`. That is a real signal and Stage 54 should use it as an input rather than
  inventing preferences from scratch.
- **`requires proprietary plugin` also appears in that field**, and it is exactly the x86-binary wall
  from the design. On this printer, hpcups needs HP's closed plugin, which will never run on ARM,
  while foo2zjs is open and works. Ranking must prefer foo2zjs here, and the text is detectable.
- **CUPS's matching is loose and produces false positives, so the ranking table is genuinely needed.**
  Measured: `MFG:HP;MDL:LaserJet 4;CMD:PCL;` returns 33 candidates including "HP Color LaserJet 4610"
  and "HP Color LaserJet 4730 MFP", which are different printers. `MDL:Stylus Photo R300` also matches
  an R3000. Narrowing is not selecting.
- **Filtered queries take 2 to 5 seconds**, because CUPS scans the entire catalogue to apply the
  filter. Driver lookup is therefore not interactive-speed, and the unfiltered catalogue costs 2.6s
  and megabytes of decoding on a fast Mac, which is a bad idea on a Zero 2 W. See the caching note
  added to Stage 54.
- An unmatched filter returns an empty result rather than an error, so callers can tell "no driver
  claims this printer" apart from "the query broke".

### Stage 16: Create and delete printers
- `CUPS-Add-Modify-Printer` and `CUPS-Delete-Printer`.
- **Done when:** a queue can be created and removed from Go, and it shows up in the CUPS web UI in
  between.
- **Status:** done, 2026-08-30
- **Proof:** a queue created from Go, read back with the right driver, then listed by cupsd's own web
  UI under both its name and its description, then deleted and confirmed gone by a typed
  `ErrNotFound`.
- **CUPS queue names cannot hold what users type.** No space, no slash, no hash, 127 characters.
  "Office Laser" is illegal. So `PrinterSpec` carries a separate `Info` field for the readable name
  and `SanitiseName` derives a legal queue name from it. PROTOCOL.md updated to say the `name` a
  connector sends is free text that core sanitises internally, and that a connector must not sanitise
  it itself. A test asserts everything `SanitiseName` produces is something `ValidPrinterName`
  accepts, so the sanitiser cannot just move the failure further down the line.
- **`printer-is-shared` defaults to false, and this heads off a real conflict.** With Avahi present,
  CUPS advertises shared queues itself. printer-cycle publishes printers through connectors, so
  leaving sharing on would put one physical printer on the network twice, under two identities, from
  the same box, with no way for a user to tell which to choose.
- **New queues are left enabled and accepting jobs.** Every path reaching AddPrinter is a user asking
  for a printer they intend to use, and a queue needing a second enabling step is a queue that
  silently swallows the first print job.
- Admin operations POST to `/admin/`, not `/`.

### Stage 17: Print-Job with a streaming body
- IPP header first, then document bytes, through an `io.Pipe`, so nothing large is ever fully
  resident.
- **Done when:** a PDF prints to the file-backed queue and the output file matches expectations.
- **Status:** done, 2026-08-30

**The memory claim, proven:**

```
job 11: sent 32 MB, peak heap growth 0.1 MB
```

A sampling goroutine watches `HeapAlloc` throughout the transfer while the document is generated on
the fly, so it exists nowhere else. If the client were buffering, the heap would follow the document.

The filter chain also verified end to end: 33 bytes of text became 172,538 bytes of PostScript with
Ghostscript's own invocation line in the header.

- **A serious finding about CUPS 2.4: raw printing fails silently.** A job submitted as
  `application/vnd.cups-raw` or `application/octet-stream` is accepted, reports
  `job-completed-successfully`, and **produces no output whatsoever.** No error, no warning, nothing
  in the log. That is the worst failure mode a print server can have, because the user is told it
  worked. Consequences recorded against Stage 35 and Stage 37.
- **`printer-is-shared: false` blocks remote submission.** CUPS refuses print jobs from a remote
  client to an unshared queue: "The printer or class is not shared." Production is unaffected, since
  core reaches cupsd over its Unix socket and that counts as local. But the dev environment talks
  TCP, so its queues must be shared, and so must any deployment pointing core at CUPS on another
  machine.
- **The Print-Job response is minimal**: job-uri, job-id, job-state, job-state-message,
  job-state-reasons, and nothing else. No byte accounting, so verifying that all 32MB actually
  reached CUPS needs `job-k-octets` from Get-Job-Attributes. Moved to Stage 18.
- **Printed text does not survive into the PostScript.** The chain embeds a font subset and draws
  glyphs, so asserting the literal string appears in the output fails. Checked rather than assumed;
  the test asserts structure instead.
- `dev/out` is now bind-mounted from the container, so tests read printed bytes directly on the host
  rather than shelling into Docker.
- **Known flake, to be fixed in Stage 18.** The 32MB job leaves CUPS grinding through Ghostscript
  after the test returns, which starved a later test in the same run and made it time out. Once
  Cancel-Job exists, the streaming test should cancel its job after measuring.

### Stage 18: Job queries and cancel
- `Get-Job-Attributes`, `Get-Jobs`, `Cancel-Job`.
- **Carried over from Stage 17:**
  - Verify `job-k-octets` matches what was sent, which is the only way to confirm every byte of a
    large document reached CUPS. The Print-Job response does not carry it.
  - Make the streaming test cancel its 32MB job after measuring. Left running, it starves the rest of
    the suite and makes a later test time out.
- **Done when:** a submitted job can be read back and cancelled mid-flight, and the streaming test no
  longer leaves CUPS busy.
- **Status:** done, 2026-08-30

**Both carried-over items closed.** Byte accounting confirms nothing was lost:

```
job 19: sent 32 MB, peak heap growth 0.1 MB
CUPS accounted for 32.0 MB of the 32 MB sent
```

  That second line matters more than it looks. A small heap is also consistent with the document
  never having been sent at all, so without it the memory test proved less than it appeared to.

- **Pause and resume added, which were not in the plan.** They earn their place three times over: they
  make cancellation deterministically testable, they fix the Stage 17 flake at the source (a paused
  queue accepts the whole 32MB but never spends CPU rasterising it), and pausing a printer is a real
  feature the dashboard will want anyway. A test also pins that a paused queue keeps *accepting*
  jobs, since a pause that quietly discarded work would be a data-loss bug.
- **CUPS redacts job names, and this is a deployment requirement rather than a test problem.**
  `job-name` and `job-originating-user-name` came back empty. The cause is CUPS's `JobPrivateValues`
  policy: it hides those attributes from any client it does not regard as the job's owner or a system
  user, returning them blank rather than refusing. **If core is not in the CUPS SystemGroup, every job
  listing shows blank names, for reasons that look nothing like the real cause.** That is now a second
  reason the installer must put core's user in `lpadmin`, alongside admin access. The dev container
  was changed to match production rather than the test being weakened.
- Cancelling an already-finished job returns `ErrNotPossible`, which callers should generally treat as
  success: the user wanted it stopped and it is stopped.

### Stage 19: Subscriptions and the event loop
- `Create-Printer-Subscription`, then a `Get-Notifications` loop with lease renewal, emitting job
  state changes on a channel.
- **Done when:** printing a document produces state change events with no polling anywhere.
- **Status:** done, 2026-08-30, **but the done-when was wrong and had to be revised.**

**Revised done-when:** printing a document produces state change events through a subscription, and
core makes one small event request rather than interrogating job state.

**The full lifecycle now arrives as events:**

```
job-created            job-state=pending
printer-state-changed
job-state-changed      job-state=processing
job-completed          job-state=completed
```

- **The design assumption was wrong, and this is the measurement that settles it. CUPS 2.4.10 does
  not honour `notify-wait`.** `Get-Notifications` returns immediately whether or not anything is
  waiting, so there is no long poll and no way to sit idle until CUPS speaks. CUPS also advertises
  `notify-get-interval: 60`, meaning it expects clients to ask once a minute, which is useless for
  showing somebody their print job moving.
- **So core polls the event stream on an interval it chooses**, adaptive: 500ms while things are
  happening, backing off to 2s after five empty replies. That is still much better than the design
  it replaces, because one small request covers every printer and every job on the box, where polling
  job state means a request per queue and grows with the number of printers.
- **Nothing above this layer is affected**, exactly as predicted when the earlier "this could force a
  rewrite" claim was walked back. Connectors still receive pushed `job.updated` notifications and
  never poll anything. The polling is entirely inside core, where nobody else can see it.
- Lease renewal happens at half the lease duration, so a missed renewal has a full period to recover
  in rather than dropping events at once. The subscription is cancelled on shutdown.
- Test runs under `-race` and covers the whole lifecycle from job-created to job-completed.

### Stage 20: Measure idle cost, and tune the intervals
- **Sharpened by Stage 19, which found the answer to the question this stage was hedging.** There is
  no fallback to write: CUPS does not long-poll, so the event loop already polls. What is left is
  measuring what that costs and choosing the intervals from evidence rather than from taste.
- Measure CPU and request volume with the loop idle, at the current defaults of 500ms active and 2s
  idle. Numbers matter here: a Pi Zero 2 W has four slow cores and this runs forever.
- If idle cost is meaningful, add a wake mechanism so core can poll immediately after submitting a
  job and idle much more slowly the rest of the time, since core already knows when it has just
  created work worth watching.
- **Done when:** idle CPU and request rate are measured and recorded in `docs/`, and the intervals
  are justified by those numbers.
- **Status:** done, 2026-08-30. Recorded in `docs/performance.md`.

```
idle window:       1m0s
requests to cupsd: 35, 0.58/s
core CPU:          22ms, 0.036% of one core
cupsd CPU:         26ms, 0.043% of one core
combined:          0.079% of one core
```

- **Decision: the intervals stand and no wake mechanism gets built.** This stage held open the option
  of core polling immediately after submitting a job so the idle interval could stretch much further.
  At 0.079% of a core there is nothing worth buying with that complexity. A Zero 2 W core is perhaps
  ten to twenty times slower, putting the loop near 1 to 2% of one core out of four. The figure
  survives being wrong by an order of magnitude, which is the only reason it is safe to lean on.
- Production will read lower still: this measurement went over TCP through Docker's bridge, while a
  real deployment uses a Unix socket on the same machine.
- **The first measurement was wrong and said so loudly**: two requests in sixty seconds, which cannot
  be true at a two second interval. CUPS's `AccessLogLevel` defaults to `config` and does not log IPP
  requests at all. Replaced with a counter inside the client, which measures the thing actually cared
  about, works in production, and does not depend on how somebody configured logging.
- `Client.Requests()` was added for this and stays, because a loop that runs forever should have its
  cost observable rather than arguable.

---

# Phase 3: Storage and identity

### Stage 21: SQLite and migrations
- `modernc.org/sqlite`, never the cgo driver, so cross-compilation stays one command.
- This is now enforced mechanically rather than by discipline: the Makefile from Stage 5 builds with
  `CGO_ENABLED=0`, so a cgo driver fails `make build-all` immediately instead of silently producing
  a binary that only runs on the build host.
- A tiny embedded migration runner.
- **Done when:** a fresh database is created and migrated on first run.
- **Status:** done, 2026-08-30
- **The pure-Go driver holds up.** `internal/store` cross-compiles with `CGO_ENABLED=0` to
  linux/arm64, linux/arm and linux/amd64, so building a Pi binary from the Mac is still one command.
  Cost: the arm64 binary grows from 1.5MB to 6.1MB, which is nothing on an SD card and cheap for
  never fighting a cross-compilation toolchain.
- **One connection, deliberately.** `SetMaxOpenConns(1)`. WAL allows many readers beside one writer,
  but exploiting that means separate read and write pools and a steady supply of SQLITE_BUSY bugs.
  printer-cycle serves a household: a few users, a handful of connectors, queries touching tens of
  rows. Serialising removes a whole category of concurrency bug at a cost that does not exist here.
- **Pragmas are read back rather than assumed.** They are set through the connection string, where a
  typo fails silently: SQLite keeps its defaults and nothing complains. A test asserts each one took
  effect, since `foreign_keys` quietly off would turn every declared relationship into a comment.
- **WAL plus `synchronous(NORMAL)` because the disk is an SD card.** FULL syncs on every commit,
  which writes far more and wears the card out faster, for protection against losing power at exactly
  the wrong instant. NORMAL still survives a process crash.
- Migration runner is deliberately dependency free: numbered SQL files, each applied once inside a
  transaction, recorded in `schema_migrations`. A test pins that reopening does not reapply them,
  since a runner that re-ran migrations would destroy data on every restart.

### Stage 22: Schema
- `users`, `printers`, `connectors`, `connector_scopes`, `connector_settings`, `identity_links`,
  `jobs`. Multi-user from the first migration, as decided.
- **Done when:** the schema is applied and documented in `docs/schema.md`.
- **Status:** done, 2026-08-30. Nine tables, documented in `docs/schema.md`.
- Tests assert the schema's **invariants**, not merely that it applies: cascade behaviour, the
  case-insensitive username collision, one external identity per connector, `cups_job_id` unique only
  among jobs that have one, and the CHECK constraints refusing nonsense.
- **Deleting a connector must not delete its jobs.** `jobs.connector_id` goes null instead. Somebody
  uninstalling a Telegram bot should not lose the record of everything they printed through it.
  Deleting a printer does cascade, since a job with no printer is a row nothing can render.
- **Orphaned scopes would be a security bug**, so connector deletion takes scopes, settings and
  identity links with it. Otherwise reinstalling a connector under the same id would silently inherit
  permissions somebody had revoked.
- **An open question was surfaced rather than decided.** See the next entry.

### OPEN DECISION, raised at Stage 22: what core stores to authenticate connectors

PROTOCOL.md section 4 specifies HMAC-SHA256 over a per-connection nonce. The secret is never
transmitted, which is what makes plaintext acceptable on a home LAN, and that property holds.

But **verifying an HMAC requires holding the secret**, so core cannot store a hash of it. The database
therefore contains, in readable form, credentials for every connector on the box. Anyone who can read
that file can impersonate all of them.

**Ed25519 would remove that entirely.** The connector generates a keypair at install, hands core the
public key, and signs the nonce. Core stores only public keys, so a copied database is worth nothing.
Same shape, same round trips, an available library in every language worth writing a connector in.
PROTOCOL.md already sends `auth` as a list, so adding it is additive, and section 4 is still marked
PROPOSED with nothing depending on it yet.

**RESOLVED 2026-08-31: Ed25519.** Mohamed chose it. PROTOCOL.md section 4 rewritten, `docs/schema.md`
updated, Stages 24 and 25 rewritten to match. The spec also gained the domain-separation detail and an
enrolment flow, since a brand new connector has no key to sign with until it registers one.

### Stage 23: Users
- Create, list, delete. Argon2id password hashing. First user is admin.
- **Done when:** users round-trip through the database and a wrong password is rejected.
- **Status:** done, 2026-08-31
- **Argon2id parameters were chosen for the target machine, not copied from a server guide.** RFC 9106
  offers a 2 GiB profile and a 64 MiB one for constrained machines. Even 64 MiB is wrong here: the box
  has 512 MiB total, shared with cupsd and with Ghostscript, which spikes hard while rasterising.
  Several people signing in at once must not be able to push it into an out-of-memory kill. So the
  RFC's low-memory option: **19 MiB, two passes, one lane.** Measured at 15ms per hash on this
  machine, which puts a Pi Zero 2 W somewhere around 150 to 290ms. Acceptable for a sign-in.
- Parameters are read back out of the stored PHC string rather than from constants, so raising the
  cost later cannot lock anybody out of an account created before the change. Tested.
- **An unknown username is charged the cost of a hash.** Returning instantly for a name that does not
  exist, while a wrong password takes a hundred milliseconds, is enough to enumerate who has an
  account on the box. Both paths return the same error and spend the same work.
- **The last administrator cannot be deleted or demoted.** Either would leave a box nobody can
  configure, recoverable only by editing the database by hand.
- **Identifiers are ULID-shaped and sort by creation time**, so listings come out in order without an
  index on a timestamp. Crockford base32, which omits I, L, O and U, because these identifiers end up
  being read aloud and typed by hand in support conversations.

### Stage 24: Connector registry and enrolment
- Install a connector record, issue a single-use enrolment token, store scopes.
- **Decided 2026-08-31: Ed25519, not HMAC.** The connector generates a keypair on first run and
  registers its public key using the enrolment token. Core stores only public keys, so it never holds
  anything that could impersonate a connector and a leaked database is worth nothing. See the open
  decision recorded under Stage 22 and PROTOCOL.md section 4.
- **Done when:** a connector can be enrolled with a single-use token, its public key is stored, and
  the token cannot be reused.
- **Status:** done, 2026-08-31
- **A new connector arrives disabled and unenrolled.** Installing something is never the same act as
  trusting it: it has no key, and nothing runs until an administrator turns it on. Enabling one that
  has not enrolled is refused outright, since an entry that looks live in the dashboard while
  rejecting every connection is worse than one that is plainly off.
- Only a **hash** of each enrolment token is stored. Core never needs the token again, only to
  recognise one being presented, so keeping it would mean storing a bearer credential for nothing.
  Same reasoning that made connector authentication Ed25519: hold the least that still works.
- **Unknown, expired, used and malformed tokens all return one error.** Anything more specific tells
  whoever is guessing which guess came closest.
- **Deleting a connector revokes its outstanding tokens**, so an unused one cannot enrol a key
  against an id somebody reinstalls later. Tested by doing exactly that.
- **Scopes are a closed set and a typo is refused.** Silently storing `printers.mangage` would produce
  a connector that looks permitted in the dashboard and is denied at every call, which is a miserable
  thing to debug.
- **A test of mine was wrong and the failure was the useful kind.** It tried to mint an expired token
  by asking for a negative lifetime, but a non-positive TTL is treated as "use the default hour", so
  nothing was ever expired and the assertion was vacuous. Rewritten to age the row and exercise the
  real expiry path.

### Stage 25: Ed25519 nonce verification
- Per-connection random nonce, Ed25519 signature verification against the stored public key.
- The signed message is domain separated: `"printer-cycle-connector-auth-v1" || 0x00 || nonce`.
  Signing bare server-supplied bytes would let a hostile core collect a signature meaningful in
  another protocol.
- **Done when:** a correct signature authenticates, a signature over a previous connection's nonce is
  refused, and a signature missing the domain prefix is refused.
- **Status:** done, 2026-08-31. `internal/connauth`, all tests under `-race`.
- **Domain separation is tested, not merely documented.** A signature over the bare nonce is refused,
  so the prefix is load bearing rather than decorative. Without that test it would be possible to
  quietly drop the prefix on one side and never notice.
- **A challenge is spent by failure as well as by success.** Leaving the nonce usable after a bad
  proof would turn one connection into unlimited attempts. Guessing an Ed25519 signature is not a
  practical attack, but there is no reason to leave unlimited tries available on purpose.
- **`Message()` is exported and its exact byte layout is pinned by a test.** Connector authors in
  other languages have to reproduce it, and the definition should come from one place rather than
  from each of them reading the specification and hoping.
- **Spec tidied while here:** `nonce` and `proof` are plain base64. The draft prefixed them with
  `b64:`, which every connector author would have stripped forever for no benefit.

### Stage 26: First-run bootstrap
- On an empty database, core prints a one-time setup token to stdout and writes it to a file with
  tight permissions. The dashboard uses it to register itself and create the first admin.
- **Done when:** a fresh install goes from empty to logged-in dashboard with no manual database work.
- **Status:** done, 2026-08-31, **for the half that exists.** The dashboard is Phase 5, so the
  browser-facing end of this cannot be demonstrated yet. Everything core owns is built and tested,
  and Stage 44 completes the sentence.

**`cmd/core` stopped being a stub.** It opens its database, performs first-run setup, and prints:

```
printer-cycle is not set up yet.

  Setup token:  PCE-2441B-RXWGG-N8763-Z6WEY

Open the dashboard and enter that token to create the first account.
```

- **The circle this breaks.** The dashboard is a connector, and connectors authenticate with a key
  core already knows. A new box knows no keys, and there is no administrator who could approve one,
  because creating the administrator is what the dashboard is for. Core breaks it by writing a
  single-use token where only somebody with access to the machine can read it: the console, and a
  file at mode 0600.
- **A fresh token on every start, with the previous one invalidated.** Not really a choice: only a
  hash of a token is stored, so an earlier one cannot be reprinted. Issuing anew each start is the
  only behaviour leaving exactly one valid token in existence, which is also the one on screen. The
  banner says so, because a user who restarts and pastes the old token deserves an explanation.
- **First-run enrolment enables the dashboard, and nothing else.** There is nobody to approve it yet,
  so a valid single-use token from the machine's own console is the authorisation. The exemption is
  deliberately narrow: it names the dashboard specifically and stops the moment any account exists.
  Both halves are tested. "Nothing else could hold a token on a fresh box anyway" is probably true and
  is not a reason to write the broader rule.
- **The database is now mode 0600.** It holds argon2id password hashes, which are expensive to attack
  but still worth nobody copying, and 0644 under `/var/lib` is a poor default. The write-ahead log and
  shared-memory files are covered too.

---

# Phase 4: The protocol server

### Stage 27: WebSocket listener
- `/v1/connector` on TCP, and optionally on a Unix socket path, same handler for both.
- **Done when:** a `websocat` client connects to both and receives the `hello` notification.
- **Status:** done, 2026-08-31. Verified with a **hand-written Python client using no WebSocket
  library at all**, which is a better check than the Go library talking to itself:

```
handshake: HTTP/1.1 101 Switching Protocols
method: hello   protocol: v1   auth: ['ed25519']   nonce bytes: 32
```

- **`cmd/core` is now a daemon.** It opens its database, performs first-run setup, serves the
  connector protocol on TCP and optionally a Unix socket, and shuts down on a signal.
- **Ports: 6310 for the connector protocol, 6311 reserved for the dashboard.** Adjacent to 631, the
  IPP port, so anyone who knows what the machine does can guess them.
- **Unix socket paths are length-checked with an explanation.** The kernel caps them at 104 bytes on
  macOS and the BSDs, 108 on Linux, and exceeding it fails with a bare "invalid argument" that says
  nothing about the cause. Found by a test whose `t.TempDir()` path was too long, which is exactly how
  an operator would meet it.
- Origin checking is off deliberately. Connectors are programs, not browsers, and arrive with no
  Origin header. Origin checks protect browsers from being used as a confused deputy; they are not
  what protects core, which is authentication.

**A shutdown bug worth recording, because the obvious fix made it worse.** Shutting down with one
connector attached took exactly five seconds. Three wrong diagnoses before instrumenting it:

1. Assumed `http.Server.Shutdown` was waiting on the hijacked connection. Reordering so connections
   close first changed nothing.
2. Added a 250ms bound falling back to `CloseNow`. Still five seconds. **`CloseNow` serialises behind
   the `Close` already in flight on the same connection**, so it waits exactly as long as the thing it
   was supposed to cut short.
3. Instrumenting showed `closeAll` was the whole five seconds and `Shutdown` took 0.6ms.

The actual cause: the WebSocket closing handshake is a round trip, waiting for the peer to answer,
and it was being performed **while holding the server's mutex**. A peer that is not reading never
answers. Now every connection closes concurrently, outside the lock, with the whole operation bounded
at 500ms; anything still waiting is abandoned, since the process is stopping and the socket goes with
it. Test asserts shutdown completes in under two seconds with a connector attached.

### Stage 28: JSON-RPC message layer
- Encode and decode, request and response correlation by id, notifications, both directions.
- **Done when:** core can call a connected client and receive its reply.
- **Status:** done, 2026-08-31. `internal/jsonrpc`, everything under `-race`.
- **Both peers are equals.** A test has each side call the other, because that is the requirement
  that ruled out plain request-and-response when the protocol was chosen: a finished print job has to
  reach the connector that submitted it without the connector asking.
- **Kept in its own package, with no WebSocket anywhere near it.** The tests wire two peers together
  through in-memory channels, so a correlation bug cannot hide behind a network.
- **Correlation is tested where it actually breaks:** twenty overlapping calls whose handlers finish
  in reverse order, so every reply arrives out of sequence. A test with one call at a time would pass
  against an implementation that simply returned the next response to arrive.
- **Unexpected errors are opaque to the peer.** An error from inside core may name a file path, a
  query, or a table, and a connector on the network has no business seeing any of it. Handlers
  returning a `*jsonrpc.Error` control the code deliberately; anything else becomes a bare internal
  error, with the detail left for the log.
- **A malformed frame does not end the connection.** One bad message from a buggy connector should
  not take down a working conversation, so it draws a parse error with a null id and the connection
  carries on. Tested by sending garbage and then a valid request over the same socket.
- **Pending calls are woken when the connection dies**, rather than hanging for the lifetime of the
  process.
- **Writes are serialised**, which is what makes the protocol's promise that job updates arrive in
  order actually true rather than aspirational.
- Wired into the WebSocket, with binary frames routed away from the message layer entirely: documents
  never travel inside JSON, since base64 inflates by a third and forces the whole file into memory.
  Until Stage 35 a binary frame is logged and ignored rather than dropping the connection.
- Every method is refused with `-32002` until Stage 29 adds authentication. Refusing by default means
  a method added later without a permission check fails closed.

### Stage 29: Handshake
- `hello` with nonce, `authenticate`, granted scopes returned.
- **Done when:** an unauthenticated client can call nothing but `authenticate`.
- **Status:** done, 2026-08-31, tested under `-race`.
- **The test client is written the way a third party would have to write one:** dial, read the
  greeting, sign the nonce, call authenticate. That is a check on the specification as much as on the
  code, since anything awkward to do from outside shows up immediately.
- **Every way of failing looks identical.** Unknown connector, one that never enrolled a key, one
  that is switched off, and a wrong signature all return the same code and the same message. A test
  compares the messages to each other rather than to a constant, because the property that matters is
  that they cannot be told apart. Otherwise anyone who can reach the port could map out which
  connectors a household has installed.
- **The challenge is spent by any attempt at all**, including one with unparseable base64. One
  connection means one attempt, whatever that attempt looked like. A connection that stayed usable
  after a failure would be an unlimited guessing seat.
- **A proof captured from another connection is refused**, which is the entire reason the nonce is per
  connection rather than per connector.
- **Connections that never authenticate are closed after 30 seconds.** One consumes a goroutine, a
  socket and some memory while able to do nothing, and on a 512MB machine letting them accumulate is
  how an idle box runs out of room.
- **An unknown connector still costs a signature verification**, against a dummy key, so a name that
  does not exist cannot be identified by how quickly it fails.

### Stage 30: Registration and manifest
- `register`, manifest validation, settings schema stored, identity policy recorded.
- **Done when:** a connector's declared settings survive a core restart.
- **Status:** done, 2026-08-31. Tested by reopening the database from disk, which is what a restart
  actually is, rather than by reading back from the same connection.

**A contradiction in the specification, found by implementing it.** PROTOCOL.md section 6 said secret
settings are "stored encrypted and never returned in plaintext to any client, including the
dashboard". Both halves were wrong:

- **A connector needs its own secrets to work.** A Telegram connector that cannot read back its bot
  token cannot talk to Telegram. The rule is now: a secret goes to the connector that owns it, and to
  nobody else. The dashboard can set one and can see that one is set, but never reads it back, so a
  token typed in last year cannot be recovered by whoever has a browser open today.
- **"Encrypted" was a claim with nowhere to keep the key.** On a box with no separate key store the
  key lives beside the data it protects. The spec now says plainly that secrets sit in a database
  file readable only by the user core runs as, which is the truth and is defensible; the previous
  wording was neither.

- **A behavioural difference across restarts, caught by a test.** The schema is stored as JSON, so a
  default written as an integer comes back as a float once reloaded. A setting read as `20` before a
  restart and `20.0` after would make comparisons behave differently on Tuesday than on Monday.
  Defaults are now normalised to their declared type on the way out.
- **Registration replaces rather than merges**, so a setting dropped in a connector's new version
  stops appearing in the dashboard instead of lingering forever.
- **A secret cannot have a default**, because that would be a secret written into the connector's own
  source code.
- Values are validated against the declared schema on write: an enum outside its options, an integer
  past its bounds, a fraction where a whole number belongs, and a key never declared are all refused.
- A malformed manifest returns the actual reason, unlike every other error in this layer. It is the
  connector author's problem to fix, and naming the offending setting helps them while telling an
  attacker nothing.

### Stage 31: Scope enforcement and IPP error translation
- One middleware, deny by default, correct JSON-RPC error on refusal.
- **Moved here from Stage 12, with a reason.** Translating `internal/ipp` errors into the JSON-RPC
  codes in PROTOCOL.md section 3 cannot be done inside the IPP package, because it is impossible from
  a status code alone: IPP answers a missing printer and a missing job with the same not-found, while
  the protocol distinguishes -32003 (unknown printer) from -32004 (unknown job). Only the caller
  knows which it asked for. The translation belongs at this boundary, where that context exists. The
  intended correspondence is recorded in the doc comment on `internal/ipp/errors.go`.
- **Done when:** a connector holding only `jobs.submit` is refused `printers.manage` with code
  -32001, and an IPP not-found from a printer operation reaches the connector as -32003.
- **Status:** done, 2026-08-31, tested under `-race`.
- **The permission table is the only place a method becomes callable.** A method absent from it does
  not exist, whatever handler somebody wrote. That makes adding a method require a decision about what
  it costs, rather than making the decision optional. A test asserts every scope named in the table is
  one core actually recognises, and the dispatcher logs an internal error if a permitted method turns
  out to have no handler.
- **One gate ahead of every handler.** Authentication, existence and permission are decided in a
  single place, so no handler can be reached by forgetting a check inside it.
- **Refusals name the scope required**, in the error's data. That tells a connector author exactly
  what to ask an administrator for, and reveals nothing, since the caller already knows what it tried.
- **`users.list` implemented as the first scoped method**, so enforcement has something real to
  enforce against instead of being tested in the abstract. Added to PROTOCOL.md as section 7c.
- **The Stage 12 deferral is resolved.** The same IPP not-found becomes -32003 or -32004 depending on
  what the caller asked about, which is exactly the context the IPP package does not have and this
  layer does. Tested by translating one error object two ways and getting two codes.
- **A CUPS permission failure is deliberately NOT reported as a scope problem.** CUPS refusing core
  almost always means core is not in the `lpadmin` group, which is an operator problem. Reporting
  -32001 would send a connector author hunting for a permission they cannot grant and do not need, so
  it becomes an internal error and a log line naming the likely cause.

### Stage 32: printers.discover and printer.discovered
- Wire the streaming discovery from Stage 14 to progressive notifications.
- **Done when:** a client sees devices arrive one at a time.
- **Status:** done, 2026-08-31, tested under `-race` against a real cupsd:

```
t=2.07s   dnssd://Virtual%20Office%20Printer._printer._tcp.local/
t=3.09s   dnssd://Virtual%20Office%20Printer._ipps._tcp.local/?uuid=415411ea-...
t=3.09s   ipps://Virtual%20Office%20Printer._ipps._tcp.local/
```

  The test reads raw frames and separates notifications from the reply by hand, the way a connector
  actually has to, and asserts the announced set and the returned set agree.
- **`driverless` removed from PROTOCOL.md section 7b.** The draft carried a boolean saying whether a
  device speaks IPP Everywhere and needs no driver. **CUPS cannot say.** `CUPS-Get-Devices` names no
  backend and exposes no such attribute, so the field could only ever have been guessed from the URI
  scheme, which is a fabrication dressed as a fact. Whether a device needs a driver is answered by
  `printers.driverCandidates`, from real data, in one place.
- **Discovery is serialised across the whole box.** It makes the SNMP backend broadcast across the
  subnet, and several connectors discovering at once would multiply that across the network while all
  asking the same machine the same question. A connector in a loop cannot flood the LAN. Callers wait
  bounded by their own context, and a caller that gives up does not leak the goroutine waiting for the
  lock.
- **Core logs structured output now.** `slog.Default()` routes through the standard log package and
  emits prose rather than key-value pairs, which is wrong for something journald collects and an
  operator greps. Noticed because a grep for `msg=` found nothing in the real binary's output while
  the same pattern worked in tests. Added `--log-level` too, since the code already had debug lines
  nothing could ever show.

### Stage 33: printers.probe
- Resolve a bare address by trying 631, then 9100, then 515, plus SNMP for the model string.
- **Done when:** giving it the container's address returns a usable device URI and a model.
- **Status:** done, 2026-08-31:

```
uri=ipp://127.0.0.1:8632/ipp/print   model="Example Printer"   transport=ipp
```

  An address in, something printable out, with the printer naming itself. That is what turns a typed
  address into one-click pairing instead of a list of eighteen thousand drivers to scroll through.
- **Ports are probed concurrently**, not in turn, so an address with nothing behind it costs one
  timeout rather than three.
- **IPP is tried first, ahead of lower-numbered ports.** A printer that speaks it can say what it is;
  JetDirect and LPD work but reveal nothing about themselves.
- **Something listening that will not describe itself is still returned as usable**, with the model
  left empty and the user picking a driver by hand. Refusing it would rule out every old printer,
  which is the hardware this project exists for.
- The virtual printer container is now published on the host, so probing is tested against a genuine
  IPP printer rather than against cupsd, which is a print server and answers quite differently.
- **`make dev-up` now creates the queues as well.** They were separate, so rebuilding the containers
  left an environment with no queues and a test run failing for reasons that looked nothing like the
  cause. Which is exactly what happened during this stage.

**A flaky test I wrote, and did not fix by loosening it.** Both discovery tests asserted that devices
arrive spread over time. That depends on how quickly CUPS happens to find things: when it finds two at
nearly the same moment they are delivered at nearly the same moment, which is correct behaviour
failing an assertion about it. It passed for two stages and then failed.

The fix was to move the timing assertion somewhere deterministic rather than to delete it. A new test
in the ipp package serves a hand-built IPP response through an httptest server that pauses mid-stream
on purpose, and asserts the first device reaches the caller before the response closes:

```
device 1 at 450ms of 610ms
device 2 at 450ms of 610ms
device 3 at 610ms of 610ms
```

  The two integration tests keep every correctness assertion and no longer guess at CUPS's scheduling.

### Stage 33b: Identify legacy printers over SNMP (ADDED 2026-08-31)
- Probing identifies a printer by asking it over IPP, which works for anything made this decade and
  not at all for the JetDirect and LPD printers that answer on 9100 and 515. Those say nothing about
  themselves over their print port, and SNMP is the only way to ask.
- **This matters more than it sounds.** An old network laser is precisely the hardware printer-cycle
  exists for, and without its make and model there is no automatic driver selection: the user is
  handed the driver list this project was built to avoid.
- Needs a minimal SNMP GET for the printer MIB (`sysDescr`, `hrDeviceDescr`, `prtGeneralPrinterName`),
  either hand-rolled or via a library. Deferred rather than done because **it cannot be tested without
  real hardware or a simulated SNMP printer agent**, the same gap already recorded against Stage 13.
- **Done when:** an address answering only on 9100 yields a make and model.
- **Status:** blocked, pairs with Phase 9

### Stage 34: printers.add and printers.remove
- Pairing, with `ppd: null` meaning use the top-ranked candidate.
- **Done when:** a discovered device becomes a working queue in one call.
- **Status:** done, 2026-08-31. The product's central promise, working:

```
queue=pctest_TestAddingAPrinterInOneCall  driver=foo2zjs:0/ppd/foo2zjs/HP-LaserJet_1018.ppd  automatic=true
```

  A device id in, a working CUPS queue out, with the driver chosen and the readable name preserved.
- **`printers.driverCandidates` and `printers.list` came with it**, since choosing a driver needs
  candidates and pairing is pointless if nothing lists the result.
- **Automatic choice avoids drivers needing a closed vendor plugin**, tested against the LaserJet
  1018, where hpcups needs HP's binary that will never run on ARM and foo2zjs is open and works. The
  ranking is still interim: prefer what CUPS marks `(recommended)`, never silently pick a proprietary
  one while an open alternative exists, otherwise take the first. Stage 54 replaces it.
- **The database row is written before CUPS is touched**, which reserves the queue name. If CUPS then
  refuses, the row is removed, so a failed attempt does not consume the name the user asked for and
  leave their second try called "Office Laser 2" for no visible reason. Tested with a driver that does
  not exist.
- **Two printers may share a name.** A household with two identical printers is ordinary; the queue
  name gets a suffix and the display name is left alone.
- **Removing tolerates a queue that is already gone.** The intended state is that it does not exist,
  and it does not.

**Two problems the tests found, neither of them in the feature being built.**

**CUPS hides unshared printers from remote clients.** A test checked the queue existed by listing
printers over IPP and found nothing, while `lpstat` inside the container showed it there. The cause is
the same sharing rule from Stage 17 in a new guise: printer-cycle creates queues unshared so CUPS does
not advertise a printer the connectors already advertise, and CUPS will not show those to a remote
client. Production reaches cupsd over a Unix socket and counts as local, so it sees everything. The
test now asks the container directly, which is ground truth either way.

**Tests were sharing one CUPS while each got a fresh database.** Two tests used the same printer name,
so one test's cleanup deleted the other's queue, which failed in a way that looked exactly like the
code losing printers. Test printers are now named after the test that creates them.

**And two of my own tests expired.** They asserted `printers.list` and `printers.add` did not exist,
which was true when written. A test that asserts a feature is absent has a shelf life; they now use
names chosen never to be implemented.

### Stage 35: jobs.submit and binary streaming
- Allocate a stream id, accept binary frames, pipe them into Print-Job.
- **Validate the document format, added after Stage 17.** On CUPS 2.4, a job whose format CUPS cannot
  filter is accepted, reported as `job-completed-successfully`, and prints nothing at all. Core must
  not pass an arbitrary format straight through: reject formats the target queue does not list in
  `document-format-supported`, so a connector gets an error instead of a user getting silence.
- **Done when:** a 50MB file prints without core's memory rising meaningfully, and an unsupported
  document format is refused rather than silently discarded.
- **Status:** done, 2026-08-31. Both halves:

```
sent 50 MB through the protocol, peak heap growth 3.8 MB
refused: application/vnd.cups-raw is accepted by CUPS and then discarded without printing
```

- **The document is never assembled anywhere.** Chunks arrive as binary frames and go straight into a
  pipe a goroutine is reading into CUPS. Writing happens on the connection's read loop, so a slow
  printer stops core reading from that connector and TCP carries the backpressure to the connector's
  own writes. A document arrives no faster than the printer can take it, and nothing queues in memory
  to disguise that.
- **Length and checksum are verified before the pipe is closed**, so a truncated upload never reaches
  the printer at all rather than being cancelled halfway through printing.
- **A connector that disconnects mid-document has its stream abandoned and its job marked aborted**,
  rather than leaving a pipe, a goroutine and a half-sent job in CUPS.

**Three findings, each of which would have broken real use.**

**The WebSocket read limit was 32KB**, the library's default, while the specification recommends 64KB
chunks. A connector following the recommendation would have been disconnected mid-document for a
reason appearing nowhere in the protocol. The 50MB test hung for five minutes before this surfaced.
Core now accepts frames up to 4MB and **PROTOCOL.md states a maximum**, which it previously did not:
naming a recommended size and no limit left every connector author guessing.

**Format validation is not enough on its own.** CUPS lists `application/vnd.cups-raw` in
`document-format-supported` and then throws such jobs away, reporting success. Checking the supported
list therefore passes exactly the case the check was added for. There is now an explicit refusal for
the formats measured to be silently discarded, alongside the supported-list check.

**Queue sharing is derived rather than configured.** CUPS refuses print jobs from a remote client to
an unshared queue, and printer-cycle deliberately creates queues unshared so CUPS does not advertise
printers the connectors already advertise. Core reaching CUPS over a Unix socket is local and keeps
them unshared; core reaching it across a network shares them, because otherwise nothing it creates
could ever be printed to. Core knows which it is, so it decides, rather than asking an operator to
know this.

**And two test-infrastructure problems worth recording, because both produced failures that looked
like code bugs.** Cleanup ran through the protocol connection, so a test dying with a broken
connection left its queue behind, and the next run modified that stale queue instead of creating a
fresh one; cleanup now goes through the container and works when the thing under test does not. And
`go test ./...` runs packages in parallel, so two packages pushing documents through one shared cupsd
starved each other; integration tests now run one package at a time.

### Stage 36: jobs.commit, integrity, and timeouts
- Verify length and SHA-256, discard abandoned streams.
- **Done when:** a truncated upload is rejected with code -32007 and leaks nothing.
- **Status:** done, 2026-09-01, tested under `-race`.
- **Integrity landed with Stage 35**: length and checksum are verified before the pipe is closed, so a
  truncated or altered upload never reaches the printer rather than being cancelled halfway through
  printing.
- **Timeouts did not, and that is the part worth recording.** Stage 35 left a `streamIdle` constant
  with a comment explaining exactly why abandoned streams must be collected, and **nothing used it**.
  A documented protection that does not exist is worse than not claiming one, because the next person
  reading that comment believes the problem is handled.
- A reaper now runs for the life of each connection, sweeping every quarter of the idle period. It
  matters most for the case that is easy to overlook: a connector which **disconnects** is already
  cleaned up when its connection ends, but one that stays connected and simply goes quiet would
  otherwise hold a pipe and a blocked goroutine indefinitely.
- **Slow is not the same as abandoned.** A connector reading a large file off a slow disk is doing
  nothing wrong, so the deadline is per chunk rather than per stream. Tested with a stream that sends
  for two seconds under a 600ms idle period and is left alone.
- Stale streams are closed outside the map lock, so waking one blocked writer does not stall every
  other stream on the same connection.

### Stage 37: job.updated push
- Fan the Stage 19 event channel out to whichever connector owns each job.
- **Detect the silent-success case, added after Stage 17.** CUPS can complete a job having produced
  nothing. A job reaching `completed` with zero impressions is almost certainly a format the filter
  chain could not handle, and it must not be reported to the user as a successful print.
- **Done when:** a connector receives state changes it never asked for, in order, and a job that
  completed without printing anything is not reported as success.
- **Status:** done, 2026-09-01:

```
job.updated  state=pending     pages=0
job.updated  state=processing  pages=0
job.updated  state=completed   pages=1
```

**A bug from Stage 19 that had made this whole feature dead on arrival.** CUPS names the job in an
event `notify-job-id`. The parser read `job-id`, which is simply not there, so every event came back
concerning job zero, matched nothing, and was discarded as "somebody printed through CUPS directly".
Job progress reached nobody.

It survived two stages because **Stage 19's test asserted what kind of events arrived and never which
job they were about.** A test that had asked "which job is this?" would have failed immediately. That
assertion now exists, with the reason attached, so removing it later looks like what it is.

- **Watching starts with the server, not with a connection.** A job outlives the connector that
  submitted it, and somebody may be watching from a different one. `Start` is separate from `Serve` so
  anything mounting the handler itself must also start the background work, rather than printing jobs
  and reporting nothing.
- **The watcher reconnects with backoff.** cupsd restarting is ordinary on a machine that also
  installs driver packages, and a print server that quietly stops reporting progress after an
  unrelated restart is worse than one that never reported.
- **A completed job is not automatically a success.** A job that carried bytes and produced no
  impressions is reported as failed and flagged, because CUPS 2.4 reports jobs it could not filter as
  completed successfully having printed nothing. Somebody walks to the printer, finds nothing, and has
  no idea which part to distrust.
- **Narrow on purpose:** a zero-byte document producing nothing is exactly right and is left alone.
  Cancelled and aborted jobs pass through as themselves. Four unit tests pin the distinctions.
- Jobs printed through CUPS directly, bypassing printer-cycle, are ignored rather than adopted.
- Deliveries are bounded per connection, so a connector that has stopped reading cannot delay updates
  to every other connector.

### Stage 38: Identity linking
- `identity.linkRequest`, `identity.resolve`, the `identity.linked` notification, code expiry.
- **Done when:** a fake connector links an external id to a user through the full flow.
- **Status:** done, 2026-09-02, tested under `-race`. Sample code: `2JG6-7F6W`.
- **The spec was missing the operation that completes the flow.** It described requesting a code and
  being told the pairing happened, and never said how a user approves one. Added as
  `identity.approve`, along with `identity.links` and `identity.revoke`.
- **Codes are shaped for people.** Eight characters of Crockford base32, which omits I, L, O and U so
  nothing is misread while being copied off a phone, and accepted in any case, with or without the
  hyphen, with stray spaces. Forty bits over a ten-minute life, so guessing is not a practical attack.
- **A connector can only resolve identities in its own namespace.** A Telegram connector cannot
  discover who a Signal identity belongs to, because the lookup is scoped to the connector making it.
- **Re-linking moves an identity rather than failing.** Somebody handing an old phone to a family
  member should not need an administrator to unpick a row.
- Unknown, expired and already-used codes are refused identically.
- Expired requests are cleared on write rather than by a background sweeper, since they are only ever
  looked up by exact code and a machine with 512MB does not need another periodic task.

### OPEN GAP, raised at Stage 38: core has no idea who a user is

`identity.approve` asserts which user is approving, and **core cannot check that claim.** It has no
user sessions: the dashboard verifies passwords and holds the session, and core sees only a connector
saying "this is Mohamed".

The interim answer is that approving requires `users.manage` rather than `identity.link`, so an
administrator has decided the connector may speak for people. That is defensible and it is still
looser than this project's own rule that core never trusts a connector's claim about a person.

**The proper fix is user sessions in core**, which is also what Stage 43 needs and what
`jobs.submit`'s `on_behalf_of` will need at Stage 40. Added as Stage 39b so it lands before anything
depends on the loose version.

### Stage 39: Settings read and change
- `settings.get`, the `settings.changed` notification, secret values write-only.
- **Done when:** editing a setting reaches a running connector without a restart, and secrets never
  come back out.
- **Status:** done, 2026-09-02, tested under `-race`.
- **The spec's scope list had no entry for connectors at all.** It covered printers, jobs, identities
  and users, which left the dashboard's own main screen with no permission describing what it does.
  Added `connectors.read` and `connectors.manage`.
- **Reading your own settings and reading everybody's are separate methods**, rather than one method
  whose required permission depends on its arguments. `settings.get` needs no scope and includes
  secrets; `connectors.list` needs `connectors.read` and excludes them. Two operations with different
  risk deserve two names, and it keeps the permission table a plain lookup.
- **The values travel with the change notification**, rather than telling the connector to come and
  ask, since it would only turn round and call `settings.get`. One message instead of two.
- **The reply to setting a value does not echo it back**, so replacing a secret is not a way to read
  the old one. Tested by writing a secret twice and checking neither appears in either reply.
- `connectors.list` reports whether each connector is `connected`, so the page can tell something
  installed from something actually running.

### Stage 39b: User sessions in core (ADDED 2026-09-02)
- Core issues a session on a verified username and password, and methods acting for a person carry
  that session instead of naming a user.
- **Why it cannot wait.** Three things already lean on core having no answer here: `identity.approve`
  takes a user id on trust (Stage 38), `on_behalf_of` will do the same (Stage 40), and the dashboard
  has to authenticate people somehow (Stage 43). Each one is individually defensible and together
  they mean the rule that core never trusts a connector's claim about a person is not actually true.
- Needs `users.authenticate` returning a session, a sessions table with expiry, and a way for a
  method to require one.
- **Done when:** `identity.approve` takes a session rather than a user id, and a connector cannot
  approve a pairing for somebody who has not signed in.
- **Status:** done, 2026-09-02, tested under `-race`. **Rule 1 in PROTOCOL.md section 0 is now
  actually true**, where before it was contradicted quietly in three places.
- **A session belongs to the connector that issued it.** One minted by the dashboard cannot be used
  by another connector that happened to see it go past, which matters because connectors are written
  by other people and share a machine.
- **Failed sign-ins are throttled per username, not per connection**, since a connector reconnecting
  is free and would otherwise reset the count. While throttled, even the correct password is refused:
  somebody guessing must not gain anything by eventually landing on it.
- **`users.authenticate` is its own scope.** Listing who has an account and being allowed to try
  their passwords are different powers, and folding the second into `users.read` would have handed it
  to anything that merely shows a user list.
- **Changing a password ends every session for that account.** Changing it is what somebody does when
  they believe it is known, and leaving sessions alive would mean the change achieves nothing against
  the person they are worried about.
- Only a hash of each session token is stored, for the same reason as enrolment tokens: core never
  needs the token again, only to recognise one.
- `identity.approve` dropped from requiring `users.manage` to `identity.link`, because it no longer
  asserts anything about anybody. The permission got *smaller* by making the design correct.

### Stage 40: On-behalf-of enforcement
- A connector declaring `identity: none` cannot attribute jobs to a user; one declaring `linked`
  can, but only where a link exists.
- **Simpler now that sessions exist (Stage 39b).** A connector submitting for somebody it has a link
  with proves nothing about them beyond the link, which core owns and can check for itself. Nothing
  here needs to take a connector's word for who a person is.
- **Done when:** a forged `on_behalf_of` is rejected with -32006.
- **Status:** done, 2026-09-03, tested under `-race`.
- **`on_behalf_of` was being parsed and silently ignored**, so every job was attributed to nobody.
  Found by looking, not by a failure: nothing complained, jobs printed, and no user could see their
  own history.
- **Three ways to say whose job it is, all verified.** A session (the dashboard), an external identity
  resolved through a link core owns (Telegram), or nothing at all, in which case it belongs to the
  connector's fallback user (AirPrint). A connector cannot name a user in any of them.
- **PROTOCOL.md changed:** `on_behalf_of` now carries an external identity rather than a user id. The
  old form made a connector name a user, which core would have had to verify anyway, and it gave the
  dashboard no way to say who was printing, since it has no external identity for the person signed in.
- A connector declaring `identity: none` is refused even for an identity that genuinely is linked. The
  declaration is not decoration: an administrator chose a fallback user on the strength of it.

**A real bug the tests caught, in a place the stage was not about.** Two attribution tests failed
because the connector was a **snapshot taken when the connection authenticated**. Anything that
changed afterwards was invisible, which meant:

- a connector registering with `identity: linked` could never use it, since the snapshot predated the
  registration;
- an administrator setting a fallback user had no effect until the connector reconnected;
- **a revoked scope kept working**, and for a connector that stays connected for weeks that is
  indistinguishable from never revoking it;
- **a disabled connector carried on**, on its existing connection.

The connector is now read from the database on every call. It costs one small query against a local
SQLite file, which is worth less than the correctness. Two tests pin the security halves: revoking a
scope and disabling a connector both take effect at once, on a connection that is already open.

---

# Phase 5: The dashboard connector

### Stage 41: Dashboard binary skeleton
- Connects to core as a connector, handles the handshake, serves static files, holds the token so
  the browser never sees it.
- **Done when:** it registers itself against a running core and serves a placeholder page.
- **Status:** done, 2026-09-03. Verified by running both binaries together from empty directories:

```
dashboard starting
not known to core yet, enrolling
enrolled with core, connecting
connected to core
```

**A gap the stage uncovered: there was no way for a connector to enrol over the protocol.** Stage 24
built the storage and the token, and no method exposed it, so a fresh dashboard could generate a key
and never register it. Added `enrol`, callable before authenticating, gated by the token, granting no
access by itself. A test pins that last part.

- **The connector key never leaves the dashboard process.** A browser tab cannot act as the dashboard
  even if somebody points one at core directly, which is what makes the dashboard a connector rather
  than a privileged path.
- **`internal/connector` is the connector side of the protocol**, reusable and unprivileged. Stage 62's
  example connector will not use it: the point of that example is to show the protocol is
  implementable in fifty lines by somebody who has never seen this repository.
- The client reconnects with backoff, because core restarting and the machine sleeping are ordinary,
  and a connector needing a person to notice and restart it is not.
- **Enrolling is reported as a step rather than a fault.** It first logged as a disconnection, which
  would have made somebody's first experience of the software look like something had gone wrong.

### Stage 42: Frontend scaffold
- Vite, React, TypeScript, Tailwind v4, shadcn/ui. Built output embedded with `go:embed`.
- **Done when:** `make build` produces one binary containing the whole frontend.
- **Status:** done, 2026-09-03. `make build` builds the interface and then the binaries; the dashboard
  is 7.2MB and serves the page from inside itself.
- **`make build` depends on the interface**, so a dashboard without one cannot be produced by
  forgetting a step. CI installs pnpm, typechecks the interface, and builds it.
- **`//go:embed all:dist`, with the `all:` prefix deliberately.** A plain embed silently skips files
  beginning with a dot or an underscore, which is the kind of omission that surfaces as one missing
  file in production and nowhere else.
- **`web/dist/.gitkeep` is committed.** `go:embed` fails outright on a directory that does not exist,
  so without it `go build` breaks for anybody who has not run the frontend build. The built output
  itself stays ignored.
- **A binary built without its interface says so**, rather than answering every request with a 404.
  That state is easy to reach while working on core, and a wall of 404s explains nothing.
- **Browser routes fall back to index.html, and `/assets` does not.** A reload on `/printers` has to
  render the page, but quietly returning HTML for a missing script would turn a broken build into a
  blank screen with no error anywhere. Verified both ways: `/printers` returns 200,
  `/assets/nope.js` returns 404.
- The page is served with `no-store`: it names hashed asset files, so a cached copy points at files a
  new build no longer has.
- Design tokens live in one `@theme` block rather than inline, and both light and dark are defined:
  this runs on somebody's own hardware and they did not choose printer-cycle's opinion about their
  screen.

### Stage 43: Browser to dashboard session layer
- Browser talks HTTP to the dashboard binary; the dashboard relays to core over the socket with its
  own credential, carrying the logged-in user.
- **Done when:** the browser never holds a connector token and cannot reach core directly.
- **Status:** done, 2026-09-03, tested under `-race`.
- **The browser gets a cookie holding a core session; the connector key stays in the process.** A
  stolen cookie is also worth less than it looks, because core binds a session to the connector that
  issued it, so it is unusable by anything except the dashboard.
- **The relay is an allowlist, not a general passthrough.** A relay that forwarded any method would
  give a page every power the dashboard connector holds, which is all of them, and would make a single
  scripting flaw equivalent to owning the machine. A test checks the dangerous ones specifically:
  `users.authenticate` (a page could try passwords), `enrol` (a page could register a key of its own).
- **The session is attached by the dashboard, never sent by the page.** A test submits a job naming
  somebody else's session and confirms the one from the cookie is what reaches core.
- Cookie is `HttpOnly` and `SameSite=Strict`, and `Secure` only when the connection already is, since
  setting it unconditionally would break the ordinary case of a Pi on a home network with no
  certificate.
- **Signing out ends the session in core, not just the cookie**, since a cookie already copied
  elsewhere would otherwise keep working.
- **A refused sign-in does not relay core's wording.** Core deliberately makes a wrong password and an
  unknown name indistinguishable; passing its message through would undo that.

**Two gaps this stage had to close to exist at all.**

**There was no `users.create`.** The setup token promised a first administrator that no method could
create. Added, with the first genuine per-*person* rule in core: on a box with no accounts anybody the
dashboard will talk to may create one, because reaching the dashboard already required the token core
printed to its own console; after that it takes an administrator's session.

**The HTTP layer was in `package main`, where it could not be tested.** Moved to
`internal/dashboard` behind a `Caller` interface, so the security properties above are tested against
a core that answers however a test needs rather than against a running one.

### Stage 44: First-run setup screen
- Consume the setup token, create the first admin, land logged in.
- **Done when:** a fresh install is usable from a browser with no terminal step after install.
- **Status:** done, 2026-09-03. Verified by running both binaries from empty directories and driving
  a browser at them; screenshots checked rather than inferred.
- **Stage 45 is absorbed into this one.** Setup, sign-in and sign-out are one experience, and shipping
  a setup screen that lands somebody at a half-built sign-in would be worse than doing both.
- **There is no setup token to type.** Core printed one and the dashboard already spent it proving it
  belongs on this machine. Asking a person to retype it would prove nothing further and would only be
  another chance to mistype something.
- **Setup signs you in with the credentials just chosen**, rather than making somebody prove a second
  later what they demonstrated a second ago.
- **The order of questions matters.** "Does this box need setting up" is asked before "is anybody
  signed in", because a reinstalled box needs setting up whether or not a stale cookie is lying
  around, and a stale cookie is exactly what somebody has after reinstalling.
- **Errors reach the page without their protocol wrapper.** Core answers "jsonrpc: -32002 this box
  already has an account", where the first two words are addressed to a program and the rest to a
  person. Only the second half belongs on a screen.
- Both colour schemes are implemented and the dark one is what the screenshots show, since this runs
  on somebody's own hardware and they did not choose printer-cycle's opinion about their screen.
- Focus outlines are kept: this is software somebody sets up once and returns to rarely, so nothing
  in it should demand a mouse.

### Stage 45: Login and logout
- **Absorbed into Stage 44.** The server half landed in Stage 43, which needed sessions before it
  could relay anything, and the screens landed in Stage 44, because setup that leads to a half-built
  sign-in is not something to ship even for one stage.
- **Done when:** sessions survive a page reload and expire sensibly.
- **Status:** done, 2026-09-03, as part of Stage 44

### Stage 46: Printers page, discovery and pairing
- Progressive list, USB and LAN together, one pair button per device, the recommended driver named
  in plain words.
- **Done when:** a discovered virtual queue is paired in one click.
- **Status:** done, 2026-09-04. Verified by driving a browser at the running stack and looking at the
  result, which is how three of the four findings below were found at all.
- **Server-sent events were built here**, because progressive discovery reaches the dashboard and
  stops. Without a push channel to the page, the screen would spin for eight seconds and then show
  everything at once, which is the exact experience the streaming work existed to avoid. Stage 49
  needs the same channel for job progress.
- A page that falls behind is skipped rather than waited for: these are updates, not instructions, and
  the next supersedes the last.
- Connections are described in words. "Plugged in over USB", not `usb://`.

**Four things found by looking at the screen. None would have failed a test.**

**One printer appeared three times.** A modern printer advertises itself as `_ipp._tcp`,
`_ipps._tcp` and `_printer._tcp`, and all three were offered as separate things to pair with,
identically named. Now collapsed, conservatively: only a shared DNS-SD service name or UUID counts as
proof, because showing one printer twice is untidy and hiding one of two is worse.

**The first version of that collapsing split the same printer in two**, because it keyed on the UUID
where present and only some announcements carry one. The service name is the identifier every
announcement shares.

**Pairing a network printer failed after twenty-four seconds** with "the printing system is not
answering". The cause was not in printer-cycle: the container could **discover** `.local` names
without being able to **resolve** them, so CUPS found the printer and then could not reach it.
`libnss-mdns` fixes it. **A minimal server install has the same gap**, which a Raspberry Pi usually
is, so Stage 58 has to install it too.

**And I blamed that failure on the wrong thing first.** The code briefly carried a confident comment
saying CUPS had refused the printer's self-signed certificate. It had not; pairing over `ipps` works
fine once names resolve. The transport preference stays because it is still the safer default, but
the explanation attached to it has been corrected. A wrong explanation in a comment outlives the
person who wrote it.

- **Pairing a driverless printer takes about twelve seconds**, because CUPS interrogates it to build a
  driver. The screen says so while it waits rather than showing a button that appears stuck.

### Stage 47: Manual add on the same page
- Type an IP, or paste a full URI for experts.
- **Done when:** an address alone produces a working queue.
- **Status:** done, 2026-09-04. `127.0.0.1:8632` typed alone identified the printer, and the queue it
  produced accepted a real PDF that the virtual printer logged receiving.
- A typed address is looked up first and offered as something to confirm, rather than turned straight
  into a queue. A string containing `://` skips that: somebody who knows exactly what they want
  should not be made to go through a lookup that guesses at it.

**A failed add left a queue behind in CUPS.** `CUPS-Add-Modify-Printer` can fail *after* creating the
queue: it makes the queue, then tries to reach the printer to work out its capabilities, and reports
an error with the queue still in place. printer-cycle rolled back its own record and left that one,
so every failed attempt added a queue it did not know about, invisible in the interface and
impossible to remove through it, while CUPS carried on advertising it. Both sides are now undone
together. Found by adding an unreachable address and then asking CUPS what it had, rather than
trusting that the error meant nothing happened.

**A printer can refuse a hostname that resolves to it.** `ipp://printer:8632` failed while
`ipp://192.168.97.3:8632` succeeded, from the same machine, with `getent` returning that exact
address for that exact name. It is the `Host` header, not resolution: the printer rejects a name it
does not recognise as its own. So a name that works everywhere else on the network can still fail
here, and the resulting error says only that the printer is not answering. The lookup step happens to
protect against this, because it sends the same `Host` the queue later will, and an address that
answers the lookup will answer the queue.

**One printer showed twice while the search ran.** Discovery announces an *update* as well as an
arrival, replacing a device first seen over `ipps` when the better `dnssd` description of it turns
up. The only thing the page could key on was the device URI, which is exactly what changes between
the two, so it appended instead of replacing and the same machine sat in the list twice until the
final reply collapsed it. Devices now carry an opaque `identity`, added to PROTOCOL.md § 7b. The test
that should have caught it was counting announcements; it now compares the announced set against the
reply, which is the property that actually matters.

**"Example Example Printer".** CUPS builds make-and-model by putting the manufacturer in front of the
model, and most printers already put it in the model themselves, so the string comes back stuttered:
"HP HP LaserJet 4000", "Brother Brother HL-2270DW". Vendor software tends to just print that. It also
becomes the default queue name, so it would have followed the printer around. A leading repeat is now
collapsed; a model that repeats a word later keeps it.

**Dev-environment limit, not a product one.** `127.0.0.1` means the container to CUPS and the host to
the dashboard, and both dev ports are bound to loopback on purpose, so no single address reaches both
halves of the stack. In production core and cupsd share a host and the seam does not exist. The two
halves were verified against the address each can actually reach.

### Stage 48: Print page
- Choose printer, upload a file, set copies, duplex, colour, and media, submit.
- **Done when:** a PDF chosen in the browser prints to the file-backed queue.
- **Status:** done, 2026-09-04. A PDF chosen in a real browser came out of the file-backed queue as
  167KB of PostScript with its text intact, having gone through the dashboard, core, CUPS and
  Ghostscript on the way.
- **Printing does not go through /api/call.** The relay carries JSON and a document is bytes, so
  there is a dedicated endpoint that streams the upload straight into the binary frames the protocol
  already defines. The document is never assembled: not in the browser's request, not in the
  dashboard, not in core. It moves 64KB at a time from the upload into a frame and out.
- `jobs.submit` and `jobs.commit` came **off** the browser allowlist as part of this. They are two
  ends of a sequence whose middle is binary frames, so a page could only ever have used them to open
  a stream it could not feed: a job stuck half-made until the sweeper collected it a minute later,
  and as many at once as the page asked for.
- The connector client gained `SendDocument`, which is what any connector needs to send anything. It
  computes the digest as the bytes go past rather than reading the document twice, because an upload
  arriving over HTTP is readable exactly once.
- **Options are three-state, not two.** "As the printer is set" is the default and is not the same as
  off. A printer configured to print double-sided should keep doing that for somebody who never
  touched the control, so an untouched option is not sent at all.
- Tabs were added here, because a print page needs somewhere to live. Later stages hang off the same
  bar.

**A tab and a button both said "Print".** The browser driver clicked the tab, and the screenshot
showed a form that looked filled in and had done nothing. The pages were also repeating their own
name as a heading directly under the tab that already said it, which is where the collision came
from. Headings removed.

**The screenshot helper had assumed signing in means no submit button is left on screen.** True until
the first screen behind the form had a submit button of its own, at which point every run timed out.
It now waits for the signed-in chrome to appear rather than for the form to disappear. Absence is a
poor thing to wait for.

**Observed once, not reproduced:** `TestPrintJobThroughTheFilterChain` timed out after 60s in a full
run, having taken 0.3s a moment earlier and in two full runs since. cupsd had just processed the
browser-submitted job. Nothing was left behind to explain it, no pending jobs and no disabled queue,
and no cause was found. Recorded rather than explained away, and rather than papering over it by
raising the timeout.

### Stage 48b: Choosing a driver by hand (ADDED 2026-09-04)
- A printer whose model cannot be worked out automatically currently has no path through the
  dashboard at all: `printers.driverCandidates` exists in the API and nothing calls it.
- Found while verifying Stage 48. Core used to ask CUPS for a driverless queue whatever the
  transport, which is CUPS interrogating the printer over IPP. A JetDirect or LPD box cannot answer
  that, so adding one produced a queue CUPS then failed to configure and an error saying only that
  the printing system was not answering. Core now refuses those and says a driver has to be chosen,
  which is honest but leaves the user nowhere to go until this stage exists.
- **This is the printer the project exists for.** An old machine found by SNMP with no device id is
  the exact case the pitch is about, and it is the one case the one-click flow cannot serve.
- Show the candidate list when automatic selection cannot decide, marking the recommended one and
  saying plainly which need a proprietary plugin.
- **Done when:** a socket printer with no device id can be added from the dashboard.
- **Status:** done, 2026-09-04. `socket://192.0.2.10:9100` typed into the address box, refused for
  want of a driver, given one from the catalogue, and the queue came out called "HP LaserJet 4100
  MFP" with the right PPD.
- Added `printers.drivers` (PROTOCOL.md section 7b): manufacturers with nothing named, drivers with
  either a make or a search. Capped at 200 by default, and it says when it cut the list.
- The refusal is recognised by **error code**, not by message text, so improving the wording cannot
  quietly turn the next step back into a dead end. Added `-32008 driver required`, and the dashboard
  now passes core's code through to the page rather than only its message.

**Measured, because the design depends on the numbers.** A full driver installation is 17,974 PPDs.
Asking for `ppd-make` alone makes cupsd collapse that to 80 manufacturers, which is a list somebody
can choose from. One manufacturer alone can be 2,904 drivers, which is not.

**CUPS honours `ppd-make` or `ppd-make-and-model`, never both.** Sending both returns everything by
that manufacturer with the model filter silently ignored: make=HP plus model="LaserJet 4" gives 2,904
rather than 147. The failure mode is a list that looks perfectly plausible and is the wrong one, so
core sends whichever filter is more selective and applies the other itself, and a test asserts every
result still matches the model.

**The expert path now confirms before it creates.** Pasting a device URI used to make a queue
outright. It produces a row to confirm instead, so both ways of adding a printer share one driver
step rather than keeping two copies of it. Seeing what is about to be added is not a cost.

**A queue was called `socket://192.0.2.10:9100`.** A printer that cannot say what it is has no name
to fall back on but its address. Choosing a driver is the moment its model becomes known, so that is
what it gets called: names now come from the driver, cleaned of the catalogue's own vocabulary.
"HP LaserJet 4100 MFP v.3010.107 Postscript (recommended)" becomes "HP LaserJet 4100 MFP", "Epson
Stylus C20 - CUPS+Gutenprint v5.3.4" becomes "Epson Stylus C20". A heuristic, written against real
catalogue entries rather than imagined ones.

**The screenshot driver pressed the wrong button twice.** Once a tab that shared a label with the
action, once the first of two "Add" buttons when the second was meant. Both produced a screenshot of
a plausible wrong thing rather than an error, which is the worst way for a check to fail. It now
takes an index.

### Stage 49: Jobs page with live status
- Driven by pushes, never by polling. Cancel button.
- **Done when:** state changes appear without a refresh.
- **Status:** done, 2026-09-05. Proven by holding a CUPS queue, submitting from outside the browser,
  and photographing the same never-reloaded page reading "Waiting on Paper Tray" with a Stop button
  and then "Printed on Paper Tray". Stop was driven from the interface too: CUPS had the job queued,
  the button cancelled it, and CUPS no longer had it.
- Added `jobs.list` and `jobs.cancel`, which the scope table had named since Stage 30 and nothing had
  ever implemented.
- The list is fetched once and everything after is a push. A job the page has never heard of, from
  another connector or another tab, makes it fetch again: still not polling, because nothing happens
  until core says something did.
- Cancelling somebody else's job needs `jobs.read.all` as well as `jobs.cancel`, and a job belonging
  to somebody else reports as unknown rather than refused, so asking is not a way to learn it exists.
- Cancelling a job that already finished is not an error. It reports the state the job reached rather
  than claiming to have stopped it, because somebody may be holding the paper.

**The documented job states were fiction.** PROTOCOL.md had promised `queued`, `rendering`,
`printing`, `done`, `failed`, `cancelled` since the spec was written. Core never emitted any of them:
it wrote IPP's own names straight into the record and into every notification, so connectors were
told `processing`, `completed` and `aborted`. A connector author switching on the documented names
would have matched nothing at all.

**And the two vocabularies were mixed inside one function.** `finishJob` returned `e.JobState.String()`
for the ordinary case and a hand-written `"failed"` for a job that printed nothing, so the same
function spoke both. The translation was missing in three separate places: the event watcher, the
terminal-state handler, and the reply to `jobs.commit`. The last of those meant the connector's own
`jobs.commit` result said `pending` where the spec said `queued`.

`rendering` is gone with the fix rather than implemented. CUPS reports `processing` while a filter
runs and while paper moves, with nothing to tell the two apart, so it could only ever have been
guessed at, and guessing is what the `driverless` field was removed for at Stage 32. `held` and
`stopped` take its place because they are real, reachable, and mean something different to somebody
looking at a queue: "the printer needs attention" is worth saying out loud.

**No migration was written for existing rows.** Nothing is released and no installation exists that
this could break, so a migration would be ceremony over data that lives only on a development
machine. If that stops being true before Stage 58 ships, it has to be written.

### Stage 50: Connectors page and the generic settings renderer
- List installed connectors, enable and disable, and render any settings schema without knowing
  anything about the connector.
- **Done when:** a connector nobody anticipated gets a working settings page for free.
- **Status:** done, 2026-09-05. Proven the only way it can be: by writing a connector in another
  language, from PROTOCOL.md alone, importing nothing from this project, declaring settings about
  weather stations and forecast footers. Its page rendered every field type and saved them, and not
  one line of dashboard code mentions it.
- Every field type renders: string with its description, enum as a list, secret as a write-only box
  that says whether one is stored, int with its bounds, bool as a switch, text as a box.
- Saved one field at a time rather than as a form, because a connector is told the moment a setting
  changes. A form that saved everything at once would hand it a token before the setting saying what
  to do with it, and an unsaved field would look identical to a saved one.
- Three `settings.changed` notifications reached the running connector while it sat idle, and the
  secret reached it in full while the dashboard saw only that one was stored. That is the rule
  section 6 was corrected to state, working.

**Nothing could add a connector at all.** Bootstrapping said new connectors are "enrolled the normal
way, through the dashboard", and there was no such way: core could create a connector record and
issue an enrolment token, and no protocol method reached either. The dashboard could list and
configure connectors it already had, and a connector nobody anticipated could not arrive. Added
`connectors.invite`, which creates and invites in one act because from an administrator's side both
are "let this program connect", and a connector created without a token is a record nobody can use.

**A connector that was switched off was told its signature was wrong.** Being disabled was
indistinguishable from not existing: both verified against a dummy key, both came back
"authentication failed". Safe, and miserable, because switched off is the ordinary state of every
newly enrolled connector, so the first thing a connector author saw after getting their signing code
exactly right was a message pointing at their crypto. The signature is now checked first and the
answer given plainly. Nothing leaks: only the holder of the private key ever reaches it. The test
that asserted all failures look identical was narrowed to the cases that share the property, with a
wrong key against a disabled connector added to it, and the new behaviour given its own test.

**A test was failing whenever the machine was busy.** It started a goroutine to write a setting, then
returned as soon as the notification arrived, while that call was still waiting for its reply.
Cleanup closed the socket underneath it and the goroutine reported a failure against a test that had
already finished. It passed alone and failed in a full run, which is why it had been written off as a
cascade twice before. It waits for the goroutine now.

**The checkbox had no id**, found by trying to press one. The wrapping label meant it worked for
anybody using it, so nothing was broken, but it was the one field type that could not be addressed.

### Stage 51: Identity link approval
- The screen where a pairing code is approved, plus the list of linked identities with revoke.
- **Done when:** a link can be approved and revoked from the browser.
- **Status:** done, 2026-09-05. A second connector, written from the spec, asked core for a pairing
  code and printed it; the code was typed into the browser, the link appeared in the list, and the
  connector was told through `identity.linked` without polling. Unlinking from the browser emptied
  the list.
- One list, in one place, because core owns the bindings. A connector decides how a code is
  delivered and nothing else, so there is no separate account page inside every connector somebody
  installs.

**Anybody holding `identity.link` could read and undo everybody's links.** The scope was the only
check, and it is held by every chat connector that pairs anyone at all: omitting the user id listed
every external identity on the box, and revoking took a row id and no questions. Now a session says
who is asking. Your own links, or anybody's if you are an administrator, or the calling connector's
own when no person is involved. Revoking refuses with "no such link" rather than a denial, so trying
ids is not a way to learn which exist.

**An internal error left no trace anywhere.** `connectors.setEnabled` returned "internal error" and
nothing was written to any log, so there was nothing to go on. Unexpected errors are deliberately
opaque on the wire, because they may name a file path, a query or a table and a connector on the
network has no business seeing those. They were opaque to the operator too. They are now logged
before being flattened, and the very next run named the cause in one line.

**Which was: a connector could not be switched on until it had enrolled.** So the order was invite,
start it, watch it be turned away, come back and switch it on, start it again. Being switched on is
an administrator saying this may run, which can be said before the thing has run once. The rule
existed because an enabled unenrolled entry would "look live and reject every connection", and the
connectors page now says "waiting to be enrolled" for precisely that state, so the reason had already
stopped being true.

**And enrolling used to force the switch off**, which would have quietly undone the decision above:
switched on in the evening, switched off by the thing itself on first contact, with nothing to say
why. Enrolling now leaves the flag alone. A new connector is still created switched off, which is
what made "enrolling does not enable anything" true in the first place.

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

### Stage 54: Candidate ranking, and caching
- The preference table as data, not code.
- **Inputs now known, measured in Stage 15 rather than guessed:**
  - `(recommended)` inside `ppd-make-and-model`, which foomatic PPDs carry. A real hint from CUPS,
    contradicting the design-session assumption that no ranking signal exists.
  - `requires proprietary plugin` in the same field, which flags drivers depending on closed vendor
    binaries. Those are the x86-only wall: rank them below any open alternative, and never pick one
    as the automatic choice on ARM.
  - Exact `MDL:` match beats a substring match. CUPS's filtering is loose: a LaserJet 4 query returns
    Color LaserJet 4610 and 4730 MFP, and a Stylus Photo R300 query returns an R3000.
- **Caching, added after Stage 15.** A filtered PPD query costs 2 to 5 seconds because CUPS scans the
  whole catalogue. The pairing screen cannot pay that on every keystroke or every page load, so
  candidate lookups need caching keyed by device id, invalidated when driver packages change.
- **Done when:** a device with several candidates gets a deterministic, sensible first choice, the
  LaserJet 1018 picks foo2zjs over hpcups, and a repeat lookup is instant.
- **Status:** todo

### Stage 55: Overrides and firmware flags
- The known-bad match list, and the flag for models needing non-redistributable firmware.
- Also flag drivers whose `ppd-make-and-model` says `requires proprietary plugin`. Distinct from the
  firmware case: firmware is downloadable given a network, whereas a proprietary plugin is an x86
  binary that will never run on ARM at all. The dashboard has to say which of the two it is, because
  one is a wait and the other is a wall.
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
- **`libnss-mdns` is required, found at Stage 46.** Avahi lets CUPS *discover* a printer over mDNS;
  *resolving* the `.local` hostname that comes back is a separate thing done by the name service
  switch. Without it, discovery works, pairing gets as far as CUPS trying to reach the printer, and
  fails after twenty-odd seconds with "Temporary failure in name resolution". A desktop Linux install
  has it; a minimal server install does not, and a Raspberry Pi is usually the latter. The installer
  must add it and set `hosts:` in `/etc/nsswitch.conf`.
- Expect `lpadmin` to print a driver deprecation warning on every queue creation. It is noise, not a
  failure, on CUPS 2.4.x. The install script should not treat it as an error, and the dashboard
  should not surface it to users.
- **Done when:** a bare container ends with cupsd running and the driver set present.
- **Status:** todo

### Stage 59: Binaries, checksums, users, directories
- Download and verify, create the system user, add it to `lpadmin`, create config and data
  directories with correct permissions.
- **The `lpadmin` membership is required for two separate reasons**, the second found in Stage 18:
  admin operations need it, and so does reading job metadata. CUPS's `JobPrivateValues` policy blanks
  `job-name` and `job-originating-user-name` for any client it does not treat as owner or system
  user, silently, so a core outside that group shows every job with no name and no owner.
- **Done when:** core can perform a CUPS admin operation with no password anywhere, and job listings
  come back with names and owners rather than blanks.
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
- **2026-08-30, after Stage 9:** Phase 1 complete. No structural change. Following the new document
  from a torn-down state surfaced a timing gap in the discovery check, now documented. The
  development environment needs only Go and Docker: no printer, no Pi.
- **2026-08-30, after Stage 10:** no structural change. Established the pattern the rest of Phase 2
  follows: unit tests run everywhere, anything needing a real cupsd is gated behind
  `PRINTER_CYCLE_TEST_CUPS` so CI never depends on a container.
- **2026-08-30, after Stage 11:** no structural change. Worth recording one trap avoided: goipp's
  named per-group fields on Message would have flattened a multi-printer response into a single
  merged printer, so group walking is mandatory for anything that returns more than one of a thing.
  That applies to Stage 13 (devices), Stage 15 (PPDs) and Stage 18 (jobs) as well.
- **2026-08-30, after Stage 12:** the IPP-to-JSON-RPC error mapping moved from Stage 12 to Stage 31.
  It cannot be done from a status code alone, because IPP reports a missing printer and a missing job
  identically while the protocol gives them different codes. Only the calling layer knows which was
  asked for. Stage 12 delivered the typed errors that mapping will consume, and the intended
  correspondence is written down in `internal/ipp/errors.go` so it does not get lost in the gap.
- **2026-08-30, after Stage 13:** PROTOCOL.md section 7b corrected: `transport` is the device URI
  scheme, and the `snmp` value it previously allowed does not exist. The spec was wrong in a way only
  implementation could reveal, which is the argument for building the IPP layer before the protocol
  server rather than after it.
- **2026-08-30, after Stage 14:** streaming discovery delivers via callback rather than the channel
  the plan named, for goroutine-lifetime and error-handling reasons recorded in the stage. Noted
  against Stage 46 that a discovered device surfaces only when the next one is found, which the
  pairing UI has to account for rather than implying the list is complete early.
- **2026-08-30, after Stage 15:** a design assumption corrected by measurement. CUPS does emit a
  ranking hint, `(recommended)` in `ppd-make-and-model`, which the design session had concluded did
  not exist; Stage 54 now uses it as an input. Also found `requires proprietary plugin` in the same
  field, which detects the x86-only driver wall automatically, and confirmed with numbers that CUPS
  matching produces false positives (LaserJet 4 matching Color LaserJet 4730 MFP), so the ranking
  table is necessary rather than merely nice. Added caching to Stage 54: filtered queries cost 2 to 5
  seconds, which no interactive screen can pay repeatedly.
- **2026-08-30, after Stage 16:** PROTOCOL.md section 7b clarified that `printers.add`'s `name` is
  free text which core sanitises into a CUPS queue name, since CUPS forbids spaces and users type
  them constantly. Recorded the `printer-is-shared: false` decision, which prevents CUPS and the
  AirPrint connector both advertising the same printer.
- **2026-08-30, after Stage 17:** found that CUPS 2.4 accepts raw and unfilterable jobs, reports them
  completed successfully, and prints nothing. Added format validation to Stage 35 and silent-success
  detection to Stage 37, because a print server that lies about success is worse than one that fails.
  Byte accounting and a fix for the flaky 32MB job both moved to Stage 18, where Cancel-Job lands.
- **2026-08-30, after Stage 18:** added pause and resume, which were not planned. They make cancel
  testable, remove the Stage 17 flake at its source, and are a feature the dashboard needs regardless.
  Recorded against Stage 59 that CUPS silently blanks job names and owners for clients outside its
  SystemGroup, which makes the `lpadmin` membership a correctness requirement and not only an
  permissions one.
- **2026-08-30, after Stage 19:** a design assumption disproved by measurement. CUPS 2.4.10 ignores
  `notify-wait` and advertises a 60 second poll interval, so core cannot sit idle waiting to be told.
  Stage 19's done-when was rewritten rather than quietly satisfied. Stage 20 changed shape as a
  result: there is no fallback left to write, only intervals to justify with numbers. The connector
  protocol is untouched, which is the whole reason that layering exists.
- **2026-08-30, after Stage 20: PHASE 2 COMPLETE.** The IPP client is done, and every assumption the
  design rested on has been measured rather than assumed. Two turned out wrong (CUPS does not
  long-poll; CUPS does emit a ranking hint) and both were corrected in place. `docs/performance.md`
  now holds the numbers behind the design decisions, so later stages can argue with evidence.
- **2026-08-30, after Stage 21:** no structural change. Recorded the size cost of the pure-Go SQLite
  driver (1.5MB to 6.1MB on arm64) and the decision to serialise database access with a single
  connection, which suits a household-scale print server and removes a class of concurrency bug.
- **2026-08-30, after Stage 22:** raised an open decision rather than settling it. HMAC connector
  authentication forces core to store every connector's secret in recoverable form, so reading the
  database file is enough to impersonate all of them. Ed25519 would leave core holding only public
  keys. The schema was built to support either, and Stage 24 implements whichever is chosen.
- **2026-08-31:** connector authentication changed from HMAC-SHA256 to Ed25519, before any code or
  connector depended on it. Core now stores only public keys. PROTOCOL.md section 4 rewritten with
  domain separation and a single-use enrolment token flow; Stages 24 and 25 rewritten to match.
- **2026-08-31, after Stage 23:** no structural change. Argon2id sized for 512 MiB shared with
  Ghostscript rather than for a server, and the timing-equalisation on unknown usernames recorded,
  since both are the kind of decision that looks arbitrary later without the reason attached.
- **2026-08-31, after Stage 24:** Ed25519 enrolment implemented as decided. Added migration
  0003 for enrolment tokens, which the Stage 22 schema did not anticipate because the flow did not
  exist until the auth decision was made.
- **2026-08-31, after Stage 25:** removed the `b64:` prefix from the spec's nonce and proof fields
  before anyone had to implement it. Small, but it is exactly the kind of thing that is free to fix
  now and permanent once connectors exist.
- **2026-08-31, after Stage 26: PHASE 3 COMPLETE.** Storage, users, connector registry, Ed25519
  enrolment and first-run setup all done. `cmd/core` is a real program rather than a stub, though it
  exits after setup because the connector server is Phase 4. Stage 26 is honestly half-finished:
  everything core owns works, and Stage 44 supplies the browser half.
- **2026-08-31, after Stage 27:** added a Unix socket path length check after hitting the kernel limit
  in a test, since the raw failure is "invalid argument" and explains nothing. Recorded the shutdown
  investigation in full: two plausible fixes made no difference before instrumenting showed the real
  cause, and `CloseNow` as a fallback for a stuck `Close` does not work at all.
- **2026-08-31, after Stage 28:** no structural change. The message layer lives in `internal/jsonrpc`
  with no transport dependency, which is what let the correlation and lifetime behaviour be tested
  without a socket in the way.
- **2026-08-31, after Stage 29:** no structural change. Added an authentication deadline that was not
  in the plan: an unauthenticated connection can do nothing but still costs resources, and on the
  target hardware that is worth closing rather than tolerating.
- **2026-08-31, after Stage 30:** PROTOCOL.md section 6 corrected on secret settings. The spec said
  secrets are never returned to any client, which would have made them useless, since a connector
  needs its own credentials to function. It also claimed encryption that had nowhere to keep a key.
  Both replaced with what is actually true and actually enforced.
- **2026-08-31, after Stage 31:** implemented `users.list` a stage early, because scope enforcement
  with no scoped method to enforce against would have been tested against nothing. Added it to
  PROTOCOL.md as section 7c.
- **2026-08-31, after Stage 32:** removed `driverless` from the discovery response in PROTOCOL.md,
  because CUPS does not report it and the field could only have been guessed. Switched core to
  structured logging with a level flag, having noticed the default logger emits prose.
- **2026-08-31, after Stage 33:** added Stage 33b for SNMP identification of legacy printers, blocked
  on hardware. IPP identification covers modern printers; the old network lasers this project targets
  answer on 9100 or 515 and say nothing about themselves, so without SNMP they get no automatic driver
  selection. Also fixed a flake I had introduced two stages earlier by asserting on CUPS's internal
  timing, moving that assertion to a synthetic stream instead of removing it.
- **2026-08-31, after Stage 34:** `printers.list` and `printers.driverCandidates` implemented here
  rather than later, because choosing a driver needs candidates and pairing is pointless if nothing
  lists the result. Recorded that CUPS hides unshared printers from remote clients, which affects any
  deployment pointing core at CUPS on another machine. Learned to stop writing tests that assert a
  feature does not exist yet.
- **2026-08-31, after Stage 35:** PROTOCOL.md gained a maximum frame size, which it had never
  specified. Added an explicit refusal list for formats CUPS advertises and silently discards, since
  checking the supported list passes the exact case the check exists for. Queue sharing is now derived
  from whether CUPS is local. Integration tests run one package at a time, and test cleanup no longer
  depends on the connection under test still working.
- **2026-09-01, after Stage 36:** found during an autonomous check that Stage 35 had left a
  `streamIdle` constant documenting a protection that was never implemented. Implemented it. The
  lesson is narrow and worth keeping: a named constant with an explanatory comment reads as a finished
  decision, so leaving one unwired hides the gap rather than marking it.
- **2026-09-01, after Stage 37:** found that Stage 19 read the wrong attribute name for a job event's
  job id, which silently disabled every job notification. The lesson is about the test rather than the
  typo: asserting that events *arrive* is not the same as asserting they *identify anything*, and only
  the second would have caught this. The missing assertion has been added.
- **2026-09-02, after Stage 38:** added `identity.approve`, `identity.links` and `identity.revoke` to
  PROTOCOL.md; the spec described requesting a code and hearing the result but never how a user
  approves one, so the flow it documented could not complete. Raised the gap that core has no notion
  of who a user is, and added Stage 39b for user sessions, because three separate features are now
  quietly relying on trusting a connector's word for it.
- **2026-09-02, after Stage 39:** added `connectors.read` and `connectors.manage` to the scope list,
  which had covered everything except the connectors themselves. Documented settings in PROTOCOL.md as
  section 8b, which had described a settings schema without ever saying how settings are read or
  written.
- **2026-09-02, after Stage 39b:** core now knows who a person is. Documented as PROTOCOL.md section
  8a, and `identity.approve` rewritten to take a session. Worth noting the permission it needs got
  smaller, not larger: it had required `users.manage` to make trusting a connector's claim an explicit
  administrative decision, and once the claim is checked rather than trusted, `identity.link` suffices.
- **2026-09-03, after Stage 40:** discovered that connector state was cached for the life of a
  connection, so revoked scopes and disabled connectors kept working. Now read fresh per call. Also
  changed `on_behalf_of` in PROTOCOL.md from a user id to an external identity, which removes the last
  place a connector could name a person.
- **2026-09-03, after Stage 41:** added the `enrol` protocol method, which the spec described in prose
  and never defined. Without it a connector's first run could not complete, which had gone unnoticed
  because nothing had run a connector's first run until now.
- **2026-09-03, after Stage 42:** the frontend is in the build path rather than beside it. Committed
  `web/dist/.gitkeep` because `go:embed` cannot point at a directory that does not exist, which would
  otherwise break `go build` for anyone without node.
- **2026-09-03, after Stage 43:** added `users.create`, without which the first administrator the
  setup token promises could not be created by anything. It carries core's first per-person rule, as
  distinct from a per-connector scope. Moved the dashboard's HTTP layer out of `package main` into
  `internal/dashboard` so its security properties could be tested rather than asserted in prose.
- **2026-09-03, after Stage 44:** Stage 45 absorbed. Verified the screens by screenshotting them
  through headless Chrome rather than assuming markup implies appearance, which is worth the two
  minutes on anything a person actually looks at.
- **2026-09-04, after Stage 46:** added server-sent events, which Stage 49 also needs. Recorded
  `libnss-mdns` as an installer requirement against Stage 58. Corrected a comment that confidently
  blamed a pairing failure on TLS when the cause was name resolution; the fix it justified was kept,
  the reasoning was not.
- **2026-09-04, after Stage 47:** added `identity` to discovered devices (PROTOCOL.md § 7b), because
  discovery announces updates and the page had nothing to key on but the field that changes. A failed
  add now undoes the CUPS side as well as its own record: the printing system can fail *after*
  creating a queue, and rolling back only half of that leaves orphans nothing can reach. Two of the
  three bugs this stage came from looking at the screen and at CUPS after an error, not from a test.
- **2026-09-04, after Stage 48:** printing got its own endpoint rather than going through the JSON
  relay, and `jobs.submit`/`jobs.commit` came off the browser allowlist because a page can only
  half-use them. Added Stage 48b: core now refuses to guess a driver for a transport it cannot
  interrogate, which is correct and leaves the oldest printers with nowhere to go until the
  dashboard can offer the candidate list.
- **2026-09-04, after Stage 48b:** added `printers.drivers` and error code -32008, and the dashboard
  now passes core's error codes to the page so a refusal can be acted on rather than only shown.
  Recorded that CUPS ignores the model filter when a make is also given, which is a silent wrong
  answer rather than an error, and is now covered by a test.
- **2026-09-05, after Stage 49:** implemented `jobs.list` and `jobs.cancel`, which the scope table had
  named since Stage 30 and nothing had ever provided. Corrected the job states, which the spec had
  documented and core had never emitted, in the three places the translation was missing. Dropped
  `rendering` rather than implement a state CUPS cannot distinguish.
- **2026-09-05, after Stage 50:** added `connectors.invite` and `connectors.setEnabled`, without
  which no connector could be added to a running system at all. Core now tells a correctly
  authenticated but switched-off connector why it was refused. Fixed a test whose goroutine outlived
  it, which had been dismissed as a cascade twice.
- **2026-09-05, after Stage 51:** identity links are now answered by who is asking rather than by the
  scope alone, which any chat connector holds. Unexpected errors are logged before being flattened to
  "internal error", which found the next two bugs by itself: a connector could not be switched on
  before enrolling, and enrolling forced the switch back off.
