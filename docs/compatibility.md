# What works

Every number here was measured against a full driver installation, the same one
`install.sh` produces on Debian. None of it has been tested against real
hardware: see [What has not been tested](#what-has-not-been-tested).

## The short version

| | |
| --- | --- |
| Drivers installed | 18,143 |
| Distinct printer models covered | 6,525 |
| Models needing firmware fetched once | 9 |
| Models with no driver that runs on ARM | 48 |

**Printers made since roughly 2015 need no driver at all.** They speak IPP
Everywhere, printer-cycle asks them what they can do, and there is nothing to
choose or install. Nothing on this page applies to them.

## Printers that need firmware

Nine models hold no firmware of their own. They load it from the host over USB
every time they are switched on, and the file cannot be redistributed by anybody
but HP, so no distribution ships it. Until the file is there the printer prints
nothing and reports no error, which is exactly how a working printer comes to
look broken.

printer-cycle recognises all nine, says so on the row before you pair, and can
fetch the file once given a network.

- HP LaserJet 1000
- HP LaserJet 1005
- HP LaserJet 1018
- HP LaserJet 1020
- HP LaserJet P1005
- HP LaserJet P1006
- HP LaserJet P1007 (loads the P1005 file)
- HP LaserJet P1008 (loads the P1006 file)
- HP LaserJet P1505

Taken from the foo2zjs package itself, which is what knows: `/usr/sbin/getweb`
names the files it can fetch and `hplj10xx_gui.tcl` maps models onto them.

## Printers with no driver that runs on ARM

Some drivers depend on a closed binary that HP publishes for Intel and AMD
processors only. On a Raspberry Pi it does not run slowly, it does not run.

**Most printers affected by this are fine anyway.** Of the 70 models in the
catalogue with such a driver, 22 also have an open one, and printer-cycle picks
the open driver over the proprietary one whenever there is a choice. It never
selects a proprietary driver automatically.

These 48 are the ones with no alternative. On an ARM board they will not print.
On an x86 machine running printer-cycle they work normally.

- HP Color LaserJet 3500
- HP Color LaserJet 3500n
- HP Color LaserJet 3550
- HP Color LaserJet 3550n
- HP Color LaserJet 3600
- HP Color LaserJet cp1217
- HP Color LaserJet Pro MFP m176n
- HP LaserJet Cp 1025nw
- HP LaserJet cp1025
- HP LaserJet m1120 MFP
- HP LaserJet m1120n MFP
- HP LaserJet m1319f MFP
- HP LaserJet p1009
- HP LaserJet p1505n pcl3
- HP LaserJet p2014n pcl3
- HP LaserJet p2035n pcl3
- HP LaserJet Pro MFP m127fw
- HP LaserJet Professional m1132 MFP
- HP LaserJet Professional m1136 MFP
- HP LaserJet Professional m1137 MFP
- HP LaserJet Professional m1138 MFP
- HP LaserJet Professional m1139 MFP
- HP LaserJet Professional m1212nf MFP
- HP LaserJet Professional m1213nf MFP
- HP LaserJet Professional m1214nfh MFP
- HP LaserJet Professional m1216nfh MFP
- HP LaserJet Professional m1217nfw MFP
- HP LaserJet Professional m1218nfg MFP
- HP LaserJet Professional m1218nfs MFP
- HP LaserJet Professional m1219nf MFP
- HP LaserJet Professional m1219nfg MFP
- HP LaserJet Professional m1219nfs MFP
- HP LaserJet Professional P 1102w
- HP LaserJet Professional p1106
- HP LaserJet Professional p1106w
- HP LaserJet Professional p1107
- HP LaserJet Professional p1107w
- HP LaserJet Professional p1108
- HP LaserJet Professional p1108w
- HP LaserJet Professional p1109
- HP LaserJet Professional p1109w
- HP LaserJet Professional p1566
- HP LaserJet Professional p1567
- HP LaserJet Professional p1568
- HP LaserJet Professional p1569
- HP LaserJet Professional p1607dn
- HP LaserJet Professional p1608dn
- HP LaserJet Professional p1609dn

Derived by asking CUPS for every driver it has, reading the
`requires proprietary plugin` marker HP's PPDs carry, and keeping only the
models where every driver in the catalogue carries it.

## What has not been tested

**No printer on this page has been plugged in.** Every printer printer-cycle has
driven has been a virtual one, and every install has been in a container. This
page describes what the driver catalogue contains and what printer-cycle does
with it, which is a different claim from "this printer works".

Two things in particular are unverified:

- **The firmware fetch has never run against the real mirror.** It is tested
  against a local server, and the failure paths are tested, including being
  offline. The manufacturer's own copies of these files are gone: the driver
  package now fetches from a third-party mirror, which is why
  `--firmware-mirror` exists.
- **Driver ranking has never been checked against hardware.** It picks correctly
  on the cases that can be checked without a printer, including choosing
  foo2zjs over hpcups for a LaserJet 1018.

## On Alpine

Alpine packages no printer drivers at all, and cannot resolve `.local` names,
because that needs a glibc component musl does not have. On Alpine, printer-cycle
serves printers that need no driver and nothing else. The installer says both
before it starts.

## Reproducing these numbers

```sh
make dev-up
docker exec printer-cycle-cups lpinfo -m | wc -l
```
