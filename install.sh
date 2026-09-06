#!/bin/sh
#
# printer-cycle installer.
#
# POSIX sh on purpose. A Raspberry Pi minimal image and an Alpine container both
# lack bash, and the one thing an installer must not do is fail before it can
# explain itself.
#
#   sh install.sh --detect     say what this machine is and what would be installed
#
set -eu

VERSION_LINE="printer-cycle installer"

# ---------------------------------------------------------------------------
# Saying things
#
# Everything goes to stderr except the report, so --detect can be piped into
# something without the commentary landing in it.
# ---------------------------------------------------------------------------

say() { printf '%s\n' "$*" >&2; }
warn() { printf 'warning: %s\n' "$*" >&2; }

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# ---------------------------------------------------------------------------
# What machine is this
# ---------------------------------------------------------------------------

# detect_arch maps uname's name for the processor onto the one Go uses, which
# is what the release binaries are named after.
#
# armv6l is included because that is what a Raspberry Pi Zero and a Pi 1 report,
# and Go's linux/arm build runs on both it and armv7l.
detect_arch() {
	machine=$(uname -m 2>/dev/null || echo unknown)
	case "$machine" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	armv7l | armv6l | armv8l | arm) echo arm ;;
	*) die "printer-cycle has no build for $machine. It runs on 64-bit ARM, 32-bit ARM and x86-64." ;;
	esac
}

# detect_family reads /etc/os-release and reduces a distribution to the family
# whose package manager it uses.
#
# ID first, then ID_LIKE. ID_LIKE is what makes this work on distributions
# nobody here has heard of: Raspberry Pi OS says ID=raspbian ID_LIKE=debian,
# Pop!_OS says ID=pop ID_LIKE="ubuntu debian", and both are apt machines. A
# derivative that sets neither is a distribution that has broken its own
# convention, and there is nothing to do but say so.
detect_family() {
	if [ ! -r /etc/os-release ]; then
		die "no /etc/os-release, so this machine will not say what it is. printer-cycle needs to know which package manager to use."
	fi

	# Read in a subshell so its variables do not leak into the installer.
	# os-release is shell syntax by specification, but it is also a file this
	# script did not write.
	id=$(. /etc/os-release 2>/dev/null && printf '%s' "${ID:-}")
	id_like=$(. /etc/os-release 2>/dev/null && printf '%s' "${ID_LIKE:-}")

	for candidate in "$id" $id_like; do
		case "$candidate" in
		debian | ubuntu | raspbian | linuxmint | pop | devuan)
			echo debian
			return 0
			;;
		fedora | rhel | centos | rocky | almalinux)
			echo rhel
			return 0
			;;
		arch | archarm | manjaro | endeavouros)
			echo arch
			return 0
			;;
		alpine)
			echo alpine
			return 0
			;;
		opensuse* | sles | suse)
			echo suse
			return 0
			;;
		esac
	done

	die "printer-cycle does not know how to install on ${id:-this system}. It knows apt, dnf, pacman, apk and zypper."
}

# detect_manager confirms the package manager is actually there.
#
# Separate from the family because the two can disagree. A Debian derivative
# with apt removed is not a Debian machine as far as an installer is concerned,
# and saying "no apt-get" is more useful than failing later inside a package
# install.
detect_manager() {
	case "$1" in
	debian) echo apt-get ;;
	rhel) if command -v dnf >/dev/null 2>&1; then echo dnf; else echo yum; fi ;;
	arch) echo pacman ;;
	alpine) echo apk ;;
	suse) echo zypper ;;
	*) die "no package manager known for family $1" ;;
	esac
}

# ---------------------------------------------------------------------------
# What to install
#
# Package names per family, verified against each distribution's own repository
# rather than written from memory. See docs/packages.md for how, and for what
# was found: the names differ more than they look, and two families split the
# drivers up differently.
# ---------------------------------------------------------------------------

# packages_core is the printing system itself, and the two pieces of name
# resolution without which a printer is found and then cannot be reached.
#
# nss-mdns is not optional anywhere it exists. Avahi lets CUPS discover a
# printer over mDNS; resolving the .local name that comes back is a separate
# thing, done by the name service switch. Without it discovery works, pairing
# gets as far as CUPS trying to reach the printer, and fails twenty seconds
# later with a name resolution error. That cost most of a stage to find once.
#
# Alpine has no nss-mdns, and cannot: it is a glibc NSS module and Alpine is
# musl, which has no such mechanism. Verified against the repositories rather
# than assumed. See limitations().
packages_core() {
	case "$1" in
	debian) echo "cups cups-client avahi-daemon libnss-mdns" ;;
	rhel) echo "cups cups-client avahi nss-mdns" ;;
	arch) echo "cups avahi nss-mdns" ;;
	alpine) echo "cups cups-client avahi" ;;
	suse) echo "cups cups-client avahi nss-mdns" ;;
	esac
}

# packages_drivers is the driver set, and it is the reason this project can say
# a printer works without asking anybody to find a driver.
#
# Driver-only split packages where a distribution offers them, never the full
# vendor suites: hplip pulls in a scanning stack and a Qt tray icon, and
# printer-driver-hpcups is the part that prints.
packages_drivers() {
	case "$1" in
	debian) echo "printer-driver-all printer-driver-cups-pdf cups-filters" ;;
	rhel) echo "foomatic-db hplip-common gutenprint-cups cups-filters" ;;
	arch) echo "gutenprint foomatic-db foomatic-db-engine foomatic-db-nonfree cups-filters" ;;
	# Alpine has no driver packages at all. Not in main, not in community: no
	# foomatic-db, no gutenprint, no hplip, nothing. cups-filters is the whole
	# of it. Checked against the repositories, twice, because it did not seem
	# possible.
	alpine) echo "cups-filters" ;;
	suse) echo "OpenPrintingPPDs manufacturer-PPDs gutenprint cups-filters" ;;
	esac
}

# limitations says what will not work here, before anybody finds out the hard
# way.
#
# Only Alpine has any, and it has two. A printer that needs a driver cannot be
# driven, because there are no drivers to install, and a printer found by name
# cannot be reached, because resolving a .local name needs a glibc NSS module
# and Alpine is musl. Modern printers are unaffected: IPP Everywhere needs no
# driver, and an address typed by hand needs no name resolution.
#
# Said rather than worked around. Neither of these is something an installer can
# fix, and a promise of "every driver, automatically" that quietly does not hold
# on one distribution is worse than a distribution that says what it is.
limitations() {
	case "$1" in
	alpine)
		echo "no-drivers no-mdns-names"
		;;
	*) echo "" ;;
	esac
}

describe_limitation() {
	case "$1" in
	no-drivers)
		echo "Alpine packages no printer drivers, so only printers that need none will work. That is any IPP Everywhere printer, which means most made since about 2015. An older printer will be found and will not print."
		;;
	no-mdns-names)
		echo "Alpine cannot resolve .local names, because that needs a glibc name service module and Alpine uses musl. Printers will still be discovered, but adding one may need its IP address rather than its name."
		;;
	esac
}

# ---------------------------------------------------------------------------
# Reporting
# ---------------------------------------------------------------------------

detect_report() {
	arch=$(detect_arch)
	family=$(detect_family)
	manager=$(detect_manager "$family")

	pretty=$(. /etc/os-release 2>/dev/null && printf '%s' "${PRETTY_NAME:-${ID:-unknown}}")

	printf 'os=%s\n' "$pretty"
	printf 'family=%s\n' "$family"
	printf 'manager=%s\n' "$manager"
	printf 'arch=%s\n' "$arch"
	printf 'core_packages=%s\n' "$(packages_core "$family")"
	printf 'driver_packages=%s\n' "$(packages_drivers "$family")"
	printf 'binary=printer-cycle-core-linux-%s\n' "$arch"

	limits=$(limitations "$family")
	printf 'limitations=%s\n' "$limits"
	for limit in $limits; do
		warn "$(describe_limitation "$limit")"
	done

	if ! command -v "$manager" >/dev/null 2>&1; then
		printf 'manager_present=no\n'
		warn "$manager is not on this machine, so nothing can be installed with it"
	else
		printf 'manager_present=yes\n'
	fi
}

usage() {
	cat >&2 <<EOF
$VERSION_LINE

  sh install.sh --detect    report what this machine is and what would be installed

EOF
}

main() {
	case "${1:---detect}" in
	--detect) detect_report ;;
	-h | --help) usage ;;
	*)
		usage
		die "unknown option $1"
		;;
	esac
}

main "$@"
