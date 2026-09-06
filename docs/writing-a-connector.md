# Writing a connector

A connector is a separate program. It talks to core over a WebSocket and cannot
change a line of printer-cycle to do its job. AirPrint, a Telegram bot, a phone
app, a thing that prints the weather every morning: all the same shape.

Start by reading [`examples/hello-printer`](../examples/hello-printer). It is 95
lines and does the whole protocol. This document answers the questions it leaves
you with. For the wire format itself, see [PROTOCOL.md](../PROTOCOL.md); this is
not a summary of it.

## Getting your connector added

An administrator adds it in the dashboard, on the Connectors page, choosing what
it may do. They get a token, once. Give that token to your connector on its first
run and never again:

```sh
node hello-printer.mjs --token PCE-XXXXX-XXXXX-XXXXX-XXXXX
```

The token is single use and lasts a day. Only a hash of it is stored, so it
cannot be shown again; if it is lost, add the connector again for a new one.

**Your first run needs two connections.** You enrol on the first and authenticate
on the second, because the authentication challenge is spent by the attempt that
carried the enrolment. This is one reconnection, once, ever. Do not treat it as
an error.

## It will not work until somebody switches it on

A newly enrolled connector is off. Enrolling proves which key belongs to which
name; an administrator decides what runs. Until they do, a correct signature gets:

```
-32002 this connector is turned off. An administrator has to switch it on in the dashboard
```

That message exists because the first version said "authentication failed", and
being off is the ordinary state of every new connector, so the first thing every
author saw after getting their signing exactly right was a message about their
crypto.

## Reconnect, and re-read the token

Core restarting, the machine sleeping, a cable: all ordinary. Loop around your
connection with a backoff. `hello-printer` does not, because it is an example and
exits when it is done.

**Read the token file at every attempt rather than once at startup.** Core issues
a fresh one each time it starts until setup is finished, and invalidates the
previous one. printer-cycle's own dashboard got this wrong and spent every eight
seconds rejecting a token it had read before core had written it.

## Ask for the fewest scopes you can

Scopes are per connector and checked on every call, freshly, so an administrator
revoking one takes effect on the call being made rather than at the next
reconnection. A denial names the method and the scope it wanted, so there is
nothing to guess:

```json
{"code":-32001,
 "message":"this connector does not hold the scope required by printers.add",
 "data":{"required_scope":"printers.manage"}}
```

A chat bot that prints wants `jobs.submit` and `identity.link`. It does not want
`printers.manage`, and asking for it means an administrator has to decide whether
to let a chat bot reconfigure their printers.

## Knowing who is printing

Your manifest declares one of two policies, and it decides everything else.

**`identity: "linked"`** means you can resolve an external identity to a
printer-cycle account. Ask core for a pairing code, deliver it however you like,
and the person types it into the dashboard while signed in as themselves:

```
identity.linkRequest  -> {"code":"2JG6-7F6W"}     you deliver this
                         person types it in the dashboard
identity.linked       <- notification, you are told
identity.resolve      -> {"user_id":"user_01H..."}
```

Then submit with `on_behalf_of` and core resolves it. **You never name a user.**
A connector can present something that proves who somebody is, or say nothing.
There is no third option where core takes your word.

**`identity: "none"`** means you cannot tell. AirPrint is this: a phone on the
network prints without authenticating, because that is what AirPrint is. Jobs go
to a fallback user an administrator chooses for your connector.

> **Not finished yet.** `connectors.setFallbackUser` exists, but nothing in the
> dashboard calls it, so an administrator currently has no way to choose. Until
> that is built, jobs from an `identity: "none"` connector have no owner and
> appear on nobody's jobs page. Tracked as Stage 63b.

## Sending a document

Open a stream, send binary frames, commit. Each frame is four bytes of stream id
then part of the file, and 64KB is the right chunk size.

The document is never held whole at either end, which is what lets a Raspberry Pi
with 512MB print a 50MB file. Do not read the file into memory to send it.

**Commit is checked.** Send the byte count, and the sha256 if you have it. Core
verifies before anything reaches the printer, so a connection that died halfway
is refused rather than half printed.

**Formats are checked too, and this one bites.** CUPS accepts a format it cannot
filter, reports the job completed successfully, and prints nothing at all. Core
refuses those instead, and tells you what the queue does take:

```json
{"code":-32007,
 "message":"Example_Tray does not accept application/x-nonsense",
 "data":{"supported":["application/pdf","application/postscript","image/jpeg", "..."]}}
```

That is core saving you from a silent success. Note the order: the printer is
looked up first, so a bad printer id gives you `-32003 no such printer` and you
never reach the format check.

## Getting job progress

You do not poll. Core pushes `job.updated` for every job you submitted, until it
reaches a terminal state:

```json
{"jsonrpc":"2.0","method":"job.updated","params":{
  "job_id":"job_01H...","state":"printing","pages_done":2,"pages_total":5
}}
```

States are `queued`, `held`, `printing`, `stopped`, `done`, `failed`,
`cancelled`. They are printer-cycle's names, not IPP's: you will never see
`processing` or `aborted`.

Updates are a stream of current state, not a queue of events. If you fall behind,
the next one supersedes the last, and one may be skipped rather than delayed.

## Settings, and secrets

Whatever your manifest declares gets a settings page in the dashboard, rendered
from the schema, with no dashboard code that knows anything about you. Field
types: `string`, `text`, `int`, `bool`, `enum`, `secret`.

`register` returns the current values. After that, core sends
`settings.changed` when an administrator edits one, with all your values, so you
do not have to ask and do not have to restart.

**A secret is readable by the connector that owns it and by nobody else.** The
dashboard can set one and can see that one is set. It cannot read it back, so a
token entered last year cannot be recovered by whoever has the browser open
today. They are not encrypted at rest, and the reason is written down in
PROTOCOL.md section 6 rather than dressed up.

## Testing without a printer

`make dev-up` gives you a CUPS container with two file-backed queues and a
virtual IPP printer in its own container. Print to `file-ps` and read the bytes
that come out. See [development.md](development.md).

For a printer that reports itself as something specific, `ippeveprinter` takes
`-M` and `-m`:

```sh
ippeveprinter -M "Hewlett-Packard" -m "HP LaserJet 1018" -f application/pdf -p 8633 "Old Laser"
```

## Things not to do

**Do not sanitise the printer name.** Core keeps what the user typed and derives
a legal queue name itself. Two printers may share a display name.

**Do not put a document in JSON.** Base64 inflates by a third and forces the
whole file into memory. There are binary frames for this reason.

**Do not count `printer.discovered` notifications.** One printer is announced
more than once: an update replaces an earlier, worse description of the same
machine. Key on `identity`, not on `device_uri`, which is the field that changes.

**Do not assume a device id identifies anything.** `CMD:PCL;` is a complete
device id in the wild. It names a language and no hardware at all.

**Do not import printer-cycle's Go packages.** They are `internal/` and will not
compile for you. That is deliberate: the protocol is the contract, and the day a
connector depends on core's source is the day the document stops being what both
sides agree on.
