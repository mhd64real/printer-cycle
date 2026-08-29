# Development

## What you need

- Go 1.24 or newer
- Docker, with Compose

That is the whole list. No printer, no Raspberry Pi. The development environment supplies both.

## Getting running

```sh
make dev-up        # build and start the containers
make dev-printers  # create the virtual queues
make build         # compile both binaries into bin/
```

## What that gives you

**Two containers.**

`printer-cycle-cups` runs Debian, cupsd 2.4.10, and the full open driver catalogue, roughly 18,000
PPDs. It is published on `127.0.0.1:6631`.

Port 6631 rather than 631, because macOS runs its own cupsd on 631 and that collision is silent and
extremely confusing when you hit it.

`printer-cycle-virtual-printer` runs `ippeveprinter`, a virtual IPP Everywhere printer advertising
itself over DNS-SD.

It is a separate container deliberately. The CUPS `dnssd` backend ignores services advertised by the
local machine, so running the virtual printer beside cupsd makes discovery appear to work while
actually finding nothing. Its own container means its own network namespace and its own address, so
cupsd discovers it the same way it would discover real hardware.

**Two queues**, both writing to files so output can be inspected rather than trusted.

| queue | driver | why it exists |
| --- | --- | --- |
| `file-ps` | Generic PostScript | the easy path, largely pass-through |
| `file-pcl` | Generic PCL laser | runs the Ghostscript rasterisation chain, which is the path every old printer depends on |

Output lands in `/var/spool/pc-out/` inside the CUPS container.

## Checking it actually works

```sh
# cupsd is up
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:6631/

# discovery finds the virtual printer
# (give it a few seconds after dev-up: mDNS advertisement is not instant, and an
#  empty result immediately after startup means "too early", not "broken")
docker exec printer-cycle-cups lpinfo -v | grep dnssd

# printing works end to end
docker exec printer-cycle-cups sh -c 'echo hello | lp -d file-pcl'
docker exec printer-cycle-cups ls -l /var/spool/pc-out/
```

The PCL output should begin with the PCL reset sequence. That is how you know the filter chain ran,
rather than bytes being copied straight through:

```sh
docker exec printer-cycle-cups od -c /var/spool/pc-out/file-pcl.out | head -1
# 0000000 033   E 033   &   l   6   D ...
```

## Things that will confuse you otherwise

**The container has no authentication, on purpose.** Production reaches cupsd over its Unix socket,
where peer credentials identify a user in the `lpadmin` group and no password is ever transmitted.
TCP has no equivalent. Dropping authentication reproduces production's observable behaviour more
faithfully than basic auth would: in both cases core sends no credentials and administrative
operations succeed.

**`lpadmin` warns about deprecated drivers.** This is expected output, not a failure:

```
lpadmin: Printer drivers are deprecated and will stop working in a future version of CUPS.
```

Debian trixie ships CUPS 2.4.10, where drivers are deprecated but fully working, and CUPS 3.0 is not
in the distribution. See PLAN.md for where that risk currently stands.

**SNMP discovery cannot be tested here.** The DNS-SD path works, via the virtual printer. SNMP is the
path that finds old network laser printers, which is exactly the hardware this project targets, and
faking it needs either real hardware or a simulated SNMP printer agent. It stays unverified until
then, and that is recorded rather than glossed over.

## Every make target

| command | what it does |
| --- | --- |
| `make build` | both binaries for this machine, into `bin/` |
| `make build-all` | release binaries for arm64, armv7 and amd64, into `dist/` |
| `make check` | what CI runs: vet, then build |
| `make vet` / `make fmt` / `make test` | the individual pieces |
| `make dev-up` | build and start the containers |
| `make dev-printers` | create the virtual queues, idempotent |
| `make dev-logs` | follow container logs |
| `make dev-shell` | a shell inside the CUPS container |
| `make dev-down` | stop and remove the containers |
| `make clean` | remove `bin/` and `dist/` |

## Layout

```
cmd/core/          the daemon: users, printers, jobs, the connector protocol
cmd/dashboard/     the web dashboard, which is a connector like any other
internal/          shared packages
web/               frontend source, built and embedded into the dashboard binary
dev/               the development environment: Dockerfile, cupsd.conf, entrypoints
docs/              this
scripts/           installer and helpers
```

## Where to look next

- `PROTOCOL.md` is the contract between core and every connector. It is the part that cannot change
  once other people's repositories depend on it.
- `PLAN.md` is the build plan, stage by stage, including a revision log explaining what reality
  forced and why.
