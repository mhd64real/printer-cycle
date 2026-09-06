# Package names, and how they were checked

`install.sh` names packages for five distribution families. Every one of those
names was checked against that distribution's own repository inside a container,
because package names are the sort of thing that feels obvious and is wrong.

Re-run the whole check with:

```sh
make check-install
```

## How

For each family, ask the package manager whether it has ever heard of the name:

| Family  | Question                    |
| ------- | --------------------------- |
| debian  | `apt-cache show NAME`       |
| rhel    | `dnf -q info NAME`          |
| arch    | `pacman -Si NAME`           |
| alpine  | `apk search -x NAME`        |
| suse    | `zypper info NAME`          |

Checked 2026-09-06 against debian:trixie-slim, fedora:41, archlinux:base,
alpine:3.21 and opensuse/leap:15.6.

## What was found

**Two names were wrong, both on Alpine.** `foomatic-db` and `foomatic-db-engine`
were in the list from memory and do not exist there.

**Alpine has no printer drivers at all.** Not in main, not in community. No
foomatic-db, no gutenprint, no hplip, no PPD collection of any kind. `cups` and
`cups-filters` are the whole of its printing stack. This was checked twice, with
the community repository explicitly enabled, because it did not seem possible.

**Alpine also has no `nss-mdns`,** and cannot have one: it is a glibc name
service switch module and Alpine is musl, which has no such mechanism. That is
the exact failure found at Stage 46, where CUPS discovers a printer over mDNS
and then cannot resolve the `.local` name it was given. On Alpine it is
permanent rather than a missing package.

So Alpine gets printer-cycle with two things said out loud at install time: only
printers needing no driver will print, which means IPP Everywhere and so most
hardware made since about 2015, and adding a printer may need its address rather
than its name. Both are printed as warnings by `install.sh --detect`.

**Arch needed `--security-opt seccomp=unconfined` to check at all**, under
emulation on an ARM host. pacman drops to a sandbox user and its seccomp filter
fails there, which has nothing to do with printer-cycle and everything to do
with running an x86 package manager on an ARM machine. Worth writing down so the
next person does not read `error: package 'cups' was not found` as a real answer.

## Why these packages

**Driver-only split packages, never the vendor suites.** `hplip` pulls a
scanning stack, a Python tray applet and Qt. `printer-driver-hpcups` is the part
that prints. Debian and Fedora both split them; Arch and openSUSE ship larger
collections and there is nothing smaller to choose.

**`nss-mdns` is not optional.** Avahi lets CUPS *discover* a printer over mDNS.
Resolving the `.local` name that comes back is a separate thing, done by the name
service switch, and without it pairing gets as far as CUPS trying to reach the
printer and fails twenty seconds later with a name resolution error. A desktop
install has it; a minimal server install, which a Raspberry Pi usually is, does
not.

**`printer-driver-all` on Debian** is a metapackage covering the split driver
packages in one name, which is exactly what this project wants: every driver,
without asking anybody to choose.
