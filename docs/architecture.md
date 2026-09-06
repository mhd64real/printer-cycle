# Why it is built this way

This is the reasoning, not the specification. [PROTOCOL.md](../PROTOCOL.md) says what the wire looks
like, [writing-a-connector.md](writing-a-connector.md) says how to use it, [schema.md](schema.md) says
what is stored and [performance.md](performance.md) has the numbers. This says why any of it is
shaped as it is, including the parts that were wrong first.

## CUPS does the printing

printer-cycle does not implement printing. CUPS and its driver ecosystem are decades of work on the
genuinely hard part, and 18,143 drivers is not a thing to reimplement. Everything here is the layer
above: discovery presented as one button, drivers chosen rather than searched for, a clean interface,
and a protocol so the rest can be written by other people.

The risk in that choice is real and was checked rather than assumed. CUPS 3.0 intends to remove
classic drivers, which is the entire foundation of driving old printers. The position on the target
platform: Debian trixie ships CUPS 2.4.10, drivers are deprecated and fully working, and CUPS 3.0 is
not in the distribution. Raspberry Pi OS follows Debian. The risk is future tense, and this project
would rather be useful now than hedge against a version nobody is running.

## Core talks to CUPS over IPP, never by running commands

Everything goes through the IPP socket. No `lpadmin`, no `lpstat`, no parsing of anybody's output.

Shelling out looks easier for about a week. Then somebody's locale changes a message, or a printer
name has a space in it, or a tool starts printing a deprecation warning to stderr and every parse
that expected clean output starts lying. `lpadmin` on CUPS 2.4 does exactly that: it prints
"Printer drivers are deprecated" on every successful queue creation.

IPP gives typed attributes and status codes instead. The cost was learning that CUPS answers
questions in ways nobody documents, which is what most of [performance.md](performance.md) records.

## Connectors are separate programs

AirPrint, a chat bot, a phone app: none of them are in core, and none can be. They are separate
processes talking over a socket, which decides several things at once.

**Nobody has to touch core to add one.** That was the original requirement and it is the reason the
protocol exists at all.

**A connector cannot take core down with it.** A plugin in-process can leak, crash, or block the
event loop. A process cannot do any of those to anything but itself.

**Language does not matter.** The example is 95 lines of Node with no dependencies, and two more
were written the same way while building this. If connectors were plugins, "written by anyone" would
have meant "written by anyone using Go".

**They cannot be trusted, and are not.** A connector holds only the scopes an administrator granted,
checked freshly on every call rather than at connection time, so revoking one takes effect on the
call being made rather than at the next reconnect.

## The dashboard is a connector

It has no privileged path into core. It authenticates with a key like anything else, holds scopes
like anything else, and everything it does is available to anything you write.

This is not purity. It is the only way to know the protocol is sufficient: if the dashboard had a
back door, the protocol would have quietly grown gaps wherever the back door was more convenient, and
nobody would have noticed until the first outside connector tried to do something ordinary.

What it costs is a real handshake before the interface works at all, and the enrolment dance on first
run. What it buys is that "anything the dashboard can do, your connector can do" is a fact rather
than an aspiration.

## One socket, not two

An early draft had a control socket and a data socket. Documents are large and JSON is not for them,
so it seemed natural to separate the two.

It was collapsed to one before implementation. Two sockets means two connections to authenticate, two
to reconnect, two to keep in step, and a correlation problem between them. One socket with two frame
types, text for JSON-RPC and binary for document bytes, does the same job with none of that.

## Documents travel as binary frames

Four bytes of stream identifier, then part of the file. The document is never assembled in memory at
either end: it moves from the connector into a frame, into a pipe, into CUPS.

Base64 in JSON would have inflated every document by a third and forced the whole thing into memory
on a machine with 512MB shared with Ghostscript. The frame limit is 4MB and the recommended chunk is
64KB, which came from measurement: the WebSocket library's default read limit was 32KB, smaller than
the chunk size the specification recommended, so a connector following the document would have been
disconnected mid-file.

## Ed25519, not a shared secret

The first draft had connectors prove possession of a shared secret over HMAC. It works, and it means
core must hold that secret in recoverable form, so read access to one database file is enough to
impersonate every connector on the machine.

Ed25519 gives the same handshake shape and the same round trips while core holds only public keys. A
copy of the database is worth nothing to whoever took it.

The signed message is domain separated: a fixed string, a zero byte, then the nonce. Without that, a
hostile core could hand a connector bytes that are really a message in some other protocol and
collect a valid signature over them.

## Core owns identity, not connectors

Every login flow anybody will write ends in the same place: binding an external identity to an
account here. So core owns that one operation, and a connector owns only how the code is delivered.

The alternative is a user table per connector, which means somebody's Telegram account and their
printer-cycle account drift apart, and no single screen can answer "what can reach my printing, and
how do I stop it". That screen exists because core owns the bindings.

A connector can present proof of who somebody is, or say nothing. There is no third option where it
names a user and core believes it.

## SQLite, one connection

One file, WAL, `MaxOpenConns(1)`. A household print server does not have a concurrency problem, and a
single writer removes an entire class of bug in exchange for nothing that matters at this scale. See
[schema.md](schema.md).

## An install script, not an image

A flashable image would be simpler to support and would decide what somebody's machine is for.
printer-cycle installs onto whatever they already run, which means detecting five package managers
and knowing the driver package names for each.

That cost is real and it is paid once. It also produced the most useful finding of the installer
work: the correct way to install Debian's driver metapackage installs nothing at all, because every
driver in it is a `Recommends`. See [packages.md](packages.md).

## Every driver, by default

The user should never see a list of eighteen thousand drivers. That means installing all of them up
front and choosing automatically, which in turn means the ranking has to be good enough that the
choice is right without asking.

The ranking is a table of weighted signals rather than code, and it reports why it chose what it did,
because "printer-cycle picked this one" is not an answer anybody can check.

## What was wrong

Kept here because a design document that only lists good decisions is a sales page.

**The specification described things that did not exist.** A `driverless` field CUPS cannot report, a
`snmp` transport with no such URI scheme, `b64:` prefixes on values that were never encoded, encrypted
secrets that were not encrypted, and a set of job states core never emitted. Each was written in
confidence and removed when something tried to use it.

**The development environment was wrong twice, and hid real bugs both times.** Its cupsd allows
administration without authentication, so core could not administer a real CUPS at all and every test
passed. Its container also lacked `libnss-mdns`, so pairing over mDNS failed for a reason that was
first blamed, in a comment, on TLS.

**Several things worked only because nothing had used them yet.** `jobs.list` and `jobs.cancel` were
named in the scope table and never implemented. `users.remove` likewise. `connectors.setFallbackUser`
existed and nothing called it, so a connector that cannot identify people had no way to be given an
owner. Each was found by building the thing that needed it.

**Two bugs were only visible on screen.** A tab and a button shared a label, and a printer appeared
twice while discovery ran because the page keyed on the field that changes. Neither would have failed
a test.
