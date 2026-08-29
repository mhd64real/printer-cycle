# printer-cycle

A print server for old printers, and for printers whose software is worse than the hardware.

[![ci](https://github.com/mhd64real/printer-cycle/actions/workflows/ci.yml/badge.svg)](https://github.com/mhd64real/printer-cycle/actions/workflows/ci.yml)

## Status

**Not working yet. There is nothing to install.**

This repository currently holds the design and the build plan, and almost no code. It is public from
the first commit because building in the open is more useful than a surprise launch, not because it
is ready.

Progress is tracked stage by stage in [PLAN.md](PLAN.md).

## What it is

Software you install on a Raspberry Pi or any Linux machine. It takes printers that have become
difficult and makes them ordinary again.

Two problems, both common:

**Your printer works, but nothing can see it.** It predates AirPrint, your phone will not find it,
and the manufacturer stopped shipping drivers three operating systems ago. printer-cycle puts it
back on the network in a form modern devices already understand.

**Your printer works, but its software is miserable.** The vendor app wants an account, a cloud
service, and a large download in order to print one page. printer-cycle gives you a clean dashboard
running on your own hardware instead, with no account, and nothing leaving your network.

## How it works

CUPS does the printing. That is deliberate. CUPS and its driver ecosystem represent decades of work
that already solves the genuinely hard part, and reimplementing it would be the quickest way to fail
at this. printer-cycle is the layer above it, and it speaks to CUPS over IPP rather than by driving
command line tools.

The core is small on purpose. It does three things: sign in, add a printer, print.

Everything else is a **connector**: a separate program, in its own repository, written by anyone,
talking to core over a documented socket protocol. AirPrint, Mopria, Samba, a Telegram bot, a mobile
app, all connectors. None of them ship with core, and none of them require a change to core in order
to exist. Install one and its settings appear in the dashboard on their own.

The dashboard is itself a connector, with no privileged access. Anything it can do, anything you
write can do too.

The protocol is specified in [PROTOCOL.md](PROTOCOL.md).

It is built to run on a Raspberry Pi Zero 2 W with 512MB of RAM, which means it will run comfortably
on whatever you already have.

## What it is not

- **It does not scan.** Scanning is a separate stack entirely and it is out of scope. Not "not yet".
  Out of scope.
- **It is not a cloud service.** No account, no server of ours, nothing phoning home.
- **It cannot revive every printer.** Some models only ever had closed source x86 drivers, and those
  will never run on an ARM board. They do work if you run printer-cycle on an old x86 machine
  instead. A compatibility list will exist before the first release.

## Development

Go and Docker, nothing else. No printer and no Raspberry Pi required: the development environment
provides a containerised CUPS with the full driver catalogue, virtual queues, and a discoverable
virtual network printer.

```sh
make dev-up && make dev-printers && make build
```

See [docs/development.md](docs/development.md).

## Licence

GPLv3. See [LICENSE](LICENSE).

Connectors are separate programs communicating with core over a documented protocol, not linked
code. Implementing that protocol places no licensing obligation on your connector. License your own
repository however you like, including commercially.
