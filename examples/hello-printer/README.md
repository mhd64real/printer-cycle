# hello-printer

A printer-cycle connector in one file, with no dependencies.

It connects, says what it is, declares one setting, and prints a document. That
is the whole protocol. Everything a real connector does is more of the same
calls.

Nothing here imports anything from printer-cycle. It was written from
[PROTOCOL.md](../../PROTOCOL.md), which is the point: a connector is a separate
program that talks to core over a socket, and you can write one in whatever
language you like without reading this repository.

95 lines of code. Node 22 or newer, because WebSocket and Ed25519 are both built
in and nothing needs installing.

## Running it

Add the connector in the dashboard, on the Connectors page, giving it
`jobs.submit` and `printers.read`. You will be handed a token, once.

```sh
node hello-printer.mjs --token PCE-XXXXX-XXXXX-XXXXX-XXXXX --print letter.pdf
```

The first run enrols the key it just generated and reconnects, because the
authentication challenge is spent by the attempt that carried the enrolment.
After that the token is never needed again:

```sh
node hello-printer.mjs --print letter.pdf
```

Options: `--core` (default `ws://127.0.0.1:6310/v1/connector`), `--id`, `--key`.

## What it shows

**The keypair is the identity.** It is generated on first run and never leaves
the machine. Core stores only the public half, so a copy of core's database is
worth nothing to whoever took it.

**Signing is domain separated.** The connector signs a fixed string, a zero
byte, then the nonce core sent. The prefix is what stops a hostile core handing
over bytes that are really a message in some other protocol and collecting a
valid signature over them.

**The manifest is how a connector gets a settings page.** Declare a setting and
it appears in the dashboard, rendered from the schema, with no dashboard code
that knows anything about this connector.

**Documents travel as binary frames, not as JSON.** Four bytes of stream id then
part of the file, as many frames as it takes. The document is never held whole
by either side, which is what lets a Raspberry Pi with 512MB print a 50MB scan.

**The commit is checked.** Core verifies the length before anything reaches the
printer, so a truncated upload is refused rather than half printed.

## What it leaves out

No reconnection, no identity linking, no job progress. A real connector wants
all three, and none of them change the shape of what is here: reconnecting is a
loop around `connect`, linking is two more calls, and progress arrives as
`job.updated` notifications on the same socket.
