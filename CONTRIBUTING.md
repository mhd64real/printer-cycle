# Contributing

## The licence, decided deliberately

printer-cycle is GPLv3, and **contributions are accepted under GPLv3 with no contributor licence
agreement**. You keep the copyright in what you write.

This was a real decision rather than a default, so here is what it means. Because every contributor
keeps their copyright, core can never be relicensed without asking all of them. A closed source
edition of printer-cycle is therefore permanently off the table, and that is the intended outcome
rather than an oversight.

It costs less than it sounds. Connectors are separate programs talking to core over a documented
protocol, not linked code, so a paid connector, a hosted service or paid support are all possible
without anybody relicensing anything. What is foreclosed is taking core itself proprietary, and
asking contributors to sign that possibility away is a poor trade for a project that wants their
help.

### Sign off your commits

Instead of a licence agreement, use the [Developer Certificate of
Origin](https://developercertificate.org/). It is a statement that you wrote the patch, or otherwise
have the right to submit it under the project's licence:

```sh
git commit -s
```

That adds a `Signed-off-by` line. That is all that is required.

## What is most useful

**Tell us what you own.** printer-cycle has never met a real printer: every printer it has driven has
been virtual and every install has been a container. A report from actual hardware is worth more than
any patch right now.

The most useful report contains the printer's device id and what happened. The interface does not
show it, so read it from CUPS on the machine printer-cycle is installed on:

```sh
lpinfo -l -v | grep -A 4 'device-id'
```

Then say whether pairing chose a driver on its own, whether it printed, and what came out. A printer
that worked is as useful as one that did not, because the compatibility list currently rests entirely
on what the driver catalogue claims rather than on anything anybody has plugged in.

**A driver that ruins a printer.** [`internal/driver/firmware.go`](internal/driver/firmware.go)
carries a known-bad list that ships deliberately empty, because every entry is a claim that a
particular driver produces garbage on a particular printer, and there is no honest way to make that
claim without having seen it. If you have seen it, that is exactly the report to open: the device id,
the ppd name, and what came out of the printer.

**Distributions.** The installer knows five package manager families. If yours is not one, or a
package name is wrong on a distribution you run, that is a small and welcome change. See
[docs/packages.md](docs/packages.md) for how the names were checked, and re-run
`make check-install`.

## Working on it

Go and Docker, nothing else. No printer and no Raspberry Pi required.

```sh
make dev-up && make dev-printers   # containerised CUPS, virtual queues, a virtual network printer
make build                          # both binaries
make check                          # what CI runs
make test-integration               # against the container
```

[docs/development.md](docs/development.md) explains the environment.
[docs/architecture.md](docs/architecture.md) explains why the code is shaped as it is, and is the
better starting point than the code.

## What this project values

These are not style rules, they are the things pull requests get discussed over.

**Measure rather than guess.** Almost every design decision here was changed by something that turned
out not to be true: a CUPS attribute that does not exist, a driver metapackage that installs nothing,
an entire authentication scheme core was missing. If a change rests on how something behaves, check
how it behaves and put the number or the output in the commit message.

**Say what is not finished.** A limitation written down is a decision. A limitation left out is a bug
somebody else finds. The plan, the README and the compatibility list all record what does not work,
and patches are welcome to add to those lists.

**Comments explain why, not what.** The code says what it does. A comment earns its place by
recording the reason, the measurement, or the thing that was tried and did not work.

**Errors are for the person reading them.** "Internal error" is not a message. If a failure can say
which of several things went wrong, it should, because the difference between "you are offline" and
"that mirror has moved" is the difference between waiting and looking elsewhere.

## What will not be merged

**Scanning.** Out of scope, permanently, not "not yet".

**Connectors in core.** AirPrint, Telegram, anything of that shape belongs in its own repository
talking over the protocol. If the protocol cannot express what your connector needs, that gap is the
bug and it is worth an issue.

**Anything that reaches CUPS by running a command.** Core speaks IPP. Parsing `lpstat` output works
until somebody's locale changes.

**Telemetry, accounts, or anything that phones home.** There is no server of ours and there is not
going to be one.
