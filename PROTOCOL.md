# printer-cycle connector protocol

**Version:** v1 (draft, not frozen)
**Status:** proposed. Decisions marked SETTLED came from design; decisions marked PROPOSED need sign-off before anything depends on them.

This document is the contract between `printer-cycle-core` and every connector. Once third-party
repos depend on it, it cannot change except additively. Treat it as the most expensive artifact in
the project.

---

## Licensing of connectors

printer-cycle core is GPLv3. **Implementing this protocol places no licensing obligation on your
connector.**

A connector is a separate program, in its own process, communicating with core over this documented
protocol. It does not link against core and it is not a derivative work of it. License your
connector however you like, including as closed source, and including as something you sell.

This is the project's stated position as copyright holder, written down so nobody has to guess at
it. It is not legal advice, and the text of the GPLv3 governs core's own code.

The protocol in this document is free for anyone to implement, in any language, on either side of
the connection, including in a competing implementation of core itself.

---

## 0. The rules this protocol exists to enforce

1. Core authenticates connectors. Connectors identify people. Core never trusts a connector's claim
   about a person beyond the scope core granted that connector.
2. Anything doable in the dashboard is doable over this protocol. The dashboard is a connector and
   gets no private backdoor.
3. A connector is a separate process. It cannot inject UI, so it declares settings and core renders
   them.
4. Core can call a connector, not only answer it. Status has to be pushed, never polled.

---

## 1. Transport (SETTLED)

WebSocket. Core listens on:

- `ws://<host>:<port>/v1/connector` over TCP, for connectors anywhere on the LAN.
- the same path over a Unix domain socket, for connectors on the box.

Both are the same protocol with the same auth and the same feature set. Which one a connector uses
is a deployment detail, not a contract difference. TCP is the only one that must exist; the Unix
bind is a config line.

**Why WebSocket and not a custom framed socket.** The goal is that a developer writes a connector
without an SDK. Custom framing means every connector author reimplements partial reads, length
prefixes, and backpressure, and some of them get it wrong. WebSocket hands them framing, keepalive,
and binary support from a library that already exists in every language worth naming. It also means
the future mobile app connects with a stock client instead of a hand-written one.

**Why not gRPC.** It moves the barrier to entry from "open a socket" to "install a protobuf
toolchain in your repo." That is the wrong tax for a plugin ecosystem.

---

## 2. Framing (SETTLED)

- **Text frames** carry exactly one JSON-RPC 2.0 message. Requests, responses, and notifications,
  in both directions.
- **Binary frames** carry document bytes. Layout: 4 bytes big-endian `stream_id`, then the chunk
  payload. Nothing else.

Documents never travel inside JSON. Base64 inflates by a third and forces the whole file into
memory, which on a 512MB Pi Zero 2 W sharing RAM with Ghostscript is how you get an OOM kill.
Chunks stream. 64KB is the recommended chunk size.

---

## 3. JSON-RPC shape (SETTLED)

Standard JSON-RPC 2.0. Both peers may send requests. Chosen because it is a published spec with
defined error semantics and existing implementations, so there is less to document and less to get
wrong than in a bespoke envelope.

```json
{"jsonrpc":"2.0","id":7,"method":"jobs.submit","params":{...}}
{"jsonrpc":"2.0","id":7,"result":{...}}
{"jsonrpc":"2.0","id":7,"error":{"code":-32001,"message":"scope denied","data":{"scope":"printers.manage"}}}
{"jsonrpc":"2.0","method":"job.updated","params":{...}}
```

Error codes: JSON-RPC reserved range for protocol faults, and application faults from -32001 down.

| code | meaning |
|---|---|
| -32001 | scope denied |
| -32002 | not authenticated |
| -32003 | unknown printer |
| -32004 | unknown job |
| -32005 | unknown stream |
| -32006 | identity not linked |
| -32007 | payload rejected (size, checksum, unsupported type) |

---

## 4. Handshake (PROPOSED)

```
core      -> connector : hello        (notification)
connector -> core      : authenticate (request)
core      -> connector : result: granted scopes
connector -> core      : register     (request, carries the manifest)
core      -> connector : result: current settings values
                         ... normal operation ...
```

**hello**

```json
{"jsonrpc":"2.0","method":"hello","params":{
  "protocol":"v1",
  "core_version":"0.1.0",
  "nonce":"9f3aBc...base64...",
  "auth":["ed25519"]
}}
```

**authenticate**

```json
{"jsonrpc":"2.0","id":1,"method":"authenticate","params":{
  "connector_id":"telegram-bot",
  "proof":"base64 of Ed25519_sign(private_key, \"printer-cycle-connector-auth-v1\\x00\" || nonce)"
}}
```

The connector signs a per-connection nonce with its private key. **Core stores only public keys**, so
it never holds anything capable of impersonating a connector, and a copied or leaked database is
worth nothing to an attacker. Nothing secret crosses the wire either, so a listener on the LAN
captures a one-time signature that is useless on the next connection. Together those are what make
plaintext acceptable on a home network without dragging certificate management onto a Raspberry Pi.

Both `nonce` and `proof` are standard base64, unadorned. An earlier draft prefixed them with `b64:`,
which every connector author would have had to strip forever for no benefit.

**The signed message is domain separated.** A connector signs the fixed ASCII string
`printer-cycle-connector-auth-v1`, a zero byte, then the raw nonce. The nonce is 32 bytes from a
cryptographically secure source, and is good for exactly one connection and one authentication
attempt. Signing bare server-supplied
bytes would let a hostile core collect a signature that meant something in a different protocol. The
prefix costs nothing and closes that off.

**Enrolment, since a new connector has nothing to sign with yet.** The admin adds the connector in
the dashboard, which issues a single-use enrolment token. The connector generates its keypair on
first run, presents the token together with its public key, and core records the key and spends the
token. From then on it signs nonces. An admin who prefers it can paste a public key in directly
instead; the token exists for convenience, not as a second trust path.

Honest limit: this authenticates the connection, not each later message, and it does not stop an
active man in the middle. TLS stays available for anyone who wants it, as a deployment option rather
than a protocol change.

**Changed 2026-08-31, from HMAC-SHA256.** The earlier draft had the connector prove possession of a
shared secret. That works, but verifying an HMAC means core must hold the secret in recoverable form,
so read access to the database file was enough to impersonate every connector on the box. Ed25519
gives the same handshake shape and the same round trips while leaving core holding only public keys.
Nothing depended on the old scheme, since section 4 was still marked PROPOSED.

Result:

```json
{"jsonrpc":"2.0","id":1,"result":{"scopes":["jobs.submit","identity.link"],"connector_id":"telegram-bot"}}
```

---

## 5. Scopes (PROPOSED)

Granted per connector, by an admin, at install time. Deny by default.

| scope | grants |
|---|---|
| `jobs.submit` | create print jobs |
| `jobs.read` | read job state (own jobs only, unless `jobs.read.all`) |
| `jobs.read.all` | read every job on the box |
| `jobs.cancel` | cancel jobs |
| `printers.read` | list and inspect printers |
| `printers.manage` | add, edit, remove printers |
| `identity.link` | start and resolve identity links |
| `users.read` | list users |
| `users.manage` | create, edit, remove users |

The dashboard connector is the only one expected to hold the full set. A Telegram connector should
hold `jobs.submit`, `jobs.read`, `identity.link` and nothing more.

---

## 6. The manifest (PROPOSED)

Sent in `register`. This is how a connector declares itself, including its settings UI, since it
cannot inject code into the dashboard.

```json
{"jsonrpc":"2.0","id":2,"method":"register","params":{
  "name":"Telegram",
  "version":"1.2.0",
  "description":"Print by sending a document to a Telegram bot.",
  "identity":"linked",
  "settings":[
    {"key":"bot_token","type":"secret","label":"Bot token","required":true},
    {"key":"allow_groups","type":"bool","label":"Accept documents in groups","default":false},
    {"key":"max_pages","type":"int","label":"Page limit per job","default":20,"min":1,"max":500}
  ]
}}
```

**`identity`** is the connector's user-identification policy:

- `"none"` - this connector does not know who anyone is. Every job it submits is attributed to the
  fallback user the admin chose for it. This is the AirPrint case: a phone on the LAN prints without
  authenticating, because that is what AirPrint is, and the connector is still authenticated to core.
- `"linked"` - this connector resolves an external identity to a printer-cycle user before
  submitting, using section 8.

**Settings field types:** `string`, `int`, `bool`, `enum` (with `options`), `secret`, `text`.
Secrets are stored encrypted and are never returned in plaintext to any client, including the
dashboard. The dashboard renders any schema it is given, so every connector past and future gets a
settings page for free.

---

## 7. Printing (PROPOSED)

```
connector -> core : jobs.submit   -> {job_id, stream_id}
connector -> core : binary frames tagged stream_id
connector -> core : jobs.commit   -> {job_id, state}
core      -> connector : job.updated (notifications until terminal)
```

**jobs.submit**

```json
{"jsonrpc":"2.0","id":3,"method":"jobs.submit","params":{
  "printer_id":"hp-laserjet-1018",
  "on_behalf_of":"user_01H...",
  "document":{"filename":"invoice.pdf","mime":"application/pdf","size":184320},
  "options":{"copies":1,"duplex":false,"color":false,"media":"A4"}
}}
```

`on_behalf_of` is honored only if the connector declared `identity: "linked"` and the link exists.
A connector declaring `identity: "none"` must omit it, and core substitutes the configured fallback
user. A connector cannot attribute a job to a user it has no link for, whatever it sends.

**jobs.commit**

```json
{"jsonrpc":"2.0","id":4,"method":"jobs.commit","params":{
  "stream_id":41,"bytes":184320,"sha256":"hex:..."
}}
```

Core verifies length and checksum before queueing. A stream with no commit within its timeout is
discarded.

**job.updated** (core to connector, notification)

```json
{"jsonrpc":"2.0","method":"job.updated","params":{
  "job_id":"job_01H...","state":"printing","pages_done":2,"pages_total":5
}}
```

States: `queued`, `rendering`, `printing`, `done`, `failed`, `cancelled`.

**Render serialization.** Core renders one job at a time, always. On a Zero 2 W, the CUPS filter
chain rasterizing a PDF for an old printer is the memory ceiling of the whole system, not the Go
process. Two concurrent renders is an OOM kill. This is architecture, not a tuning knob.

---

## 7b. Discovery and pairing (PROPOSED)

Core implements no discovery of its own. `CUPS-Get-Devices` runs every CUPS backend in discovery
mode and returns USB, mDNS, SNMP, JetDirect, and LPD devices in one list. Core relays that.

**printers.discover**

```json
{"jsonrpc":"2.0","id":10,"method":"printers.discover","params":{"timeout_ms":8000}}
```

Results arrive progressively as `printer.discovered` notifications, then the request resolves with
the complete set. The SNMP backend has to wait out a subnet broadcast, so a blocking call takes
five to ten seconds. Clients render devices as they arrive; they never block on the result.

```json
{"jsonrpc":"2.0","method":"printer.discovered","params":{
  "device_uri":"usb://HP/LaserJet%201018?serial=KP123",
  "device_id":"MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;",
  "make_and_model":"HP LaserJet 1018",
  "transport":"usb",
  "driverless":false
}}
```

`transport` is the device URI's scheme: `usb`, `dnssd`, `ipp`, `ipps`, `socket`, `lpd`, `serial`.

**Corrected 2026-08-30, after implementing discovery.** An earlier draft listed `snmp` as a possible
value. There is no `snmp://` scheme. `transport` says how printer-cycle will TALK to a device, not
how it was FOUND, and a printer the SNMP backend discovered comes back as `socket` or `lpd`, because
that is how you print to it. CUPS reports no attribute naming the discovering backend, so any field
claiming to would have been fiction.

`device_id` is frequently empty, and that is not a fault. Driverless IPP Everywhere printers have no
need of one, and some backends never ask. Automatic driver selection depends on it, so a device
without one goes down the manual path rather than the one-click path.

`make_and_model` is empty when the backend could not identify the hardware. CUPS sends the literal
string `Unknown` in that case; core normalises it away, because rendering "Unknown" under a heading
that says Model reads as a manufacturer name.

**printers.probe** resolves a bare address the user typed by hand. Core tries 631 (IPP), then 9100
(JetDirect), then 515 (LPD), and queries SNMP for the model so driver selection still works from
nothing but an IP.

```json
{"jsonrpc":"2.0","id":11,"method":"printers.probe","params":{"address":"192.168.1.50"}}
```

**printers.driverCandidates** returns the ranked PPD candidates for a device.

```json
{"jsonrpc":"2.0","id":12,"method":"printers.driverCandidates","params":{
  "device_id":"MFG:Hewlett-Packard;MDL:HP LaserJet 1018;CMD:ZJS;"
}}
```

CUPS narrows by device id for free, because PPDs carry a `1284DeviceID` field and `CUPS-Get-PPDs`
accepts a `ppd-device-id` filter. Narrowing is not selecting, though: measured against a full driver
installation, a LaserJet 4 device id returns 33 candidates including a Color LaserJet 4730 MFP, which
is a different printer. So core applies its own ranking plus an override list for matches that look
correct and print garbage. The first candidate is what the one-click pair button uses.

Two signals arrive inside `ppd-make-and-model` and both are ranking inputs:

- `(recommended)`, which foomatic PPDs carry, is CUPS's own hint.
- `requires proprietary plugin` marks a driver that depends on a closed vendor binary. Those are
  x86-only and will never run on an ARM board, so they must never be the automatic choice there.

```json
{"result":{"candidates":[
  {"ppd":"foo2zjs:HP-LaserJet_1018.ppd","score":100,"recommended":true,
   "requires_firmware":true,
   "note":"Needs a proprietary firmware blob downloaded on first use. Box must be online once."},
  {"ppd":"drv:///sample.drv/generpcl.ppd","score":20,"recommended":false}
]}}
```

**printers.add** is the pair button.

```json
{"jsonrpc":"2.0","id":13,"method":"printers.add","params":{
  "device_uri":"usb://HP/LaserJet%201018?serial=KP123",
  "name":"Office laser",
  "ppd":null
}}
```

`ppd` null means "use the top-ranked candidate", which is the whole point of the one-click flow.
Passing a ppd explicitly is the manual override. Core issues `CUPS-Add-Modify-Printer` and returns
the printer id. Requires `printers.manage`.

**`name` is free text and stays free text.** CUPS queue names may not contain a space, a slash, or a
hash, so "Office laser" is not a legal queue name. Core keeps what the user typed as the printer's
display name and derives a sanitised queue name from it internally. Connectors never see or need the
sanitised form, and a connector must not sanitise names itself.

**printers.remove** takes a printer id and issues `CUPS-Delete-Printer`.

Note on naming: AirPrint appears in both directions in this system. Core *consumes* AirPrint
printers found via `dnssd` during discovery, and the AirPrint *connector* publishes core's queues
back out to phones. Same word, opposite directions. Keep them distinct in copy and in code.

---

## 8. Identity linking (PROPOSED)

Every connector login flow anyone will ever write ends in the same place: binding an external
identity to a printer-cycle account. So core owns that one operation and the connector owns only
how the code is delivered and collected. Interactive chat login, a portal link, a QR code, and
whatever the next developer invents are all the same primitive with different delivery.

**identity.resolve**

```json
{"jsonrpc":"2.0","id":5,"method":"identity.resolve","params":{"external_id":"tg:887312"}}
```

Returns `{"user_id":"user_01H..."}` or error -32006.

**identity.linkRequest**

```json
{"jsonrpc":"2.0","id":6,"method":"identity.linkRequest","params":{
  "external_id":"tg:887312","display":"@mhd64","ttl_seconds":600
}}
```

Returns `{"code":"J4K-7QP","expires_at":"..."}`. The connector delivers that code however it likes.
The user approves it in the dashboard, core writes the binding, and core notifies the connector:

```json
{"jsonrpc":"2.0","method":"identity.linked","params":{
  "external_id":"tg:887312","user_id":"user_01H..."
}}
```

Because every binding lives in core, there is one screen that answers "what is linked to my account
and how do I revoke it." If each connector rolled its own auth end to end, there would be N user
tables, N security bugs, and no such screen.

---

## 9. Core to connector calls (PROPOSED)

| method | kind | meaning |
|---|---|---|
| `hello` | notification | sent on connect, carries the nonce |
| `settings.changed` | notification | an admin edited this connector's settings |
| `job.updated` | notification | job state moved |
| `identity.linked` | notification | a pending link was approved |
| `shutdown` | notification | core is stopping, close cleanly |

Liveness uses WebSocket ping and pong. No application-level heartbeat.

---

## 10. Versioning (SETTLED)

- The protocol version is in the path: `/v1/connector`.
- Within v1, changes are additive only. New methods, new optional fields, new scopes.
- Unknown fields are ignored, never rejected. Unknown methods return JSON-RPC -32601.
- Removing or repurposing anything requires `/v2/`, and core serves both during a transition.

Third parties will depend on this. Everything else in printer-cycle is recoverable. This is not.

---

## CUPS integration (SETTLED)

Core speaks IPP to `cupsd` over its Unix socket for everything, including administration. No
`lp`, no `lpadmin`, no `cups-client` dependency, no fork-exec anywhere.

`lpadmin` is itself a thin IPP client, so shelling out to it means forking a process to send a
request over a socket core is already connected to. Admin operations are real IPP requests:
`CUPS-Add-Modify-Printer`, `CUPS-Delete-Printer`, `CUPS-Get-Devices`, `CUPS-Get-PPDs`,
`CUPS-Create-Local-Printer`.

Parsing `lpstat` was never an option: its output is human-readable text that varies by CUPS version
and by locale. IPP gives structured attributes and structured status codes, which map cleanly onto
the error codes in section 3. IPP subscriptions also give real job events, so `job.updated` in
section 7 is a genuine push rather than a polling loop dressed up as one.

Library: `github.com/OpenPrinting/goipp`, maintained by the same project that maintains CUPS.

**Packaging consequence, needed from day one:** CUPS gates admin operations behind authentication.
Core runs as a system user in the `lpadmin` group and connects over the Unix socket, so peer
credentials satisfy it and no password exists anywhere. The installer creates that user and group
membership.

---

## Open, not yet decided

- Rate limiting and per-connector job quotas.
- Whether `printers.manage` should be splittable per printer rather than global.
- TLS story for anyone exposing the TCP socket beyond a trusted LAN.
