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
# ca-certificates and curl are here for a reason that is easy to miss. A bare
# debian:trixie-slim has neither, and neither does a minimal server install,
# which a Raspberry Pi usually is. Without curl this script cannot download its
# own binaries. Without ca-certificates, core cannot fetch printer firmware
# either: Go's HTTP client uses the system certificate pool, so an https mirror
# fails with a certificate error on a machine that has no certificates. Checked,
# not assumed.
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
	debian) echo "cups cups-client avahi-daemon libnss-mdns ca-certificates curl" ;;
	rhel) echo "cups cups-client avahi nss-mdns ca-certificates curl" ;;
	arch) echo "cups avahi nss-mdns ca-certificates curl" ;;
	alpine) echo "cups cups-client avahi ca-certificates curl" ;;
	suse) echo "cups cups-client avahi nss-mdns ca-certificates curl" ;;
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
	# printer-driver-all is expanded rather than installed: see expand_drivers.
	#
	# The two PPD collections are named separately because printer-driver-all
	# does not recommend them, and they are most of the catalogue: with the
	# metapackage's own list alone a fresh install offers 6,754 drivers, and
	# with these it offers about eighteen thousand. They are the difference
	# between "most printers" and "your printer".
	debian) echo "printer-driver-all printer-driver-cups-pdf cups-filters foomatic-db-engine foomatic-db-compressed-ppds openprinting-ppds ghostscript" ;;
	rhel) echo "foomatic-db foomatic-db-ppds hplip-common gutenprint-cups cups-filters ghostscript" ;;
	arch) echo "gutenprint foomatic-db foomatic-db-engine foomatic-db-nonfree foomatic-db-ppds cups-filters ghostscript" ;;
	# Alpine has no driver packages at all. Not in main, not in community: no
	# foomatic-db, no gutenprint, no hplip, nothing. cups-filters is the whole
	# of it. Checked against the repositories, twice, because it did not seem
	# possible.
	alpine) echo "cups-filters" ;;
	suse) echo "OpenPrintingPPDs manufacturer-PPDs gutenprint cups-filters ghostscript" ;;
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
# Installing
# ---------------------------------------------------------------------------

require_root() {
	if [ "$(id -u)" -ne 0 ]; then
		die "this has to run as root, because it installs packages and creates system directories. Try: sudo sh install.sh"
	fi
}

# refresh_index updates the package list.
#
# Its own step because a stale index is the most common reason an install fails
# on a machine that has been sitting in a cupboard, and the message it produces
# otherwise is about a package not existing.
refresh_index() {
	case "$1" in
	apt-get) DEBIAN_FRONTEND=noninteractive apt-get update -qq ;;
	dnf | yum) "$1" -q makecache ;;
	pacman) pacman -Sy --noconfirm >/dev/null ;;
	apk) apk update -q ;;
	zypper) zypper -q --non-interactive refresh ;;
	esac
}

# expand_drivers turns Debian's driver metapackage into the packages it names.
#
# printer-driver-all is a metapackage with no Depends at all: every driver is a
# Recommends. So installing it with --no-install-recommends installs nothing,
# and the install finishes with a running cupsd, no error, and 43 drivers where
# there should be thousands. That is the exact shape of failure this project
# exists to prevent, produced by the installer itself.
#
# The obvious fix is to allow recommends, and it is wrong: doing that pulls in
# 297 packages including the whole SANE scanning stack. printer-cycle does not
# scan, and dragging a scanner subsystem onto a Raspberry Pi to print is the
# vendor-suite behaviour this is meant to be an alternative to.
#
# So Debian's curated list of drivers is used, without Debian's opinion about
# what those drivers should drag along: read the names out of the metapackage,
# install those, recommends still off. Measured at 165 packages and zero
# scanning packages.
#
# Read from the metapackage rather than written down here, so a driver added to
# Debian arrives without anybody editing this file.
expand_drivers() {
	drivers=$(apt-cache show printer-driver-all 2>/dev/null |
		sed -n 's/^Recommends: //p' | head -1 | tr -d ' ' | tr ',' ' ')
	if [ -z "$drivers" ]; then
		warn "could not read the driver list out of printer-driver-all, so only the drivers it depends on directly will be installed"
		echo printer-driver-all
		return 0
	fi
	echo "$drivers"
}

# install_packages installs a list, without asking anybody anything.
#
# Every one of these is the non-interactive form. An installer that stops on a
# prompt nobody is there to answer has hung as far as its user is concerned.
install_packages() {
	manager=$1
	shift
	[ $# -gt 0 ] || return 0

	say "installing: $*"
	case "$manager" in
	apt-get) DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@" ;;
	dnf | yum) "$manager" install -y "$@" ;;
	pacman) pacman -S --needed --noconfirm "$@" ;;
	apk) apk add --no-cache "$@" ;;
	zypper) zypper --non-interactive install --no-recommends "$@" ;;
	esac
}

# configure_mdns puts mDNS into the name service switch.
#
# Installing libnss-mdns is not enough on its own: the package has to be named
# in /etc/nsswitch.conf before anything resolves a .local address through it.
# Debian's package does this itself; the others do not, and a machine where
# discovery works and pairing then fails with a name resolution error is the
# result. That symptom cost most of a stage to track down once.
#
# mdns4_minimal with [NOTFOUND=return] before dns is the standard ordering:
# .local names are answered by mDNS, everything else falls through to DNS
# without waiting for a multicast query to time out first.
configure_mdns() {
	family=$1

	# Skipped where the module cannot exist.
	#
	# Alpine ships an /etc/nsswitch.conf and musl does not implement the name
	# service switch at all, so editing it there writes a line naming a glibc
	# module that can never load. It would be inert rather than harmful, which
	# is worse than either: a configuration file that looks configured and does
	# nothing is how somebody spends an afternoon.
	case " $(limitations "$family") " in
	*" no-mdns-names "*)
		say "not configuring mDNS name resolution: this system has no module for it"
		return 0
		;;
	esac

	conf=/etc/nsswitch.conf
	if [ ! -f "$conf" ]; then
		warn "no $conf, so mDNS name resolution cannot be configured. Printers found by name may not be reachable by name."
		return 0
	fi
	if grep -q 'mdns4_minimal' "$conf"; then
		say "mDNS name resolution is already configured"
		return 0
	fi

	# Rewritten through a temporary file and moved into place, so an
	# interrupted install cannot leave a machine unable to resolve anything at
	# all, which would be a considerably worse problem than a printer.
	tmp="$conf.printer-cycle.$$"
	sed 's/^hosts:.*/hosts: files mdns4_minimal [NOTFOUND=return] dns mdns4/' "$conf" >"$tmp"

	if ! grep -q 'mdns4_minimal' "$tmp"; then
		rm -f "$tmp"
		warn "could not find a hosts line in $conf to edit. Printers found by name may not be reachable by name."
		return 0
	fi
	cat "$conf" >"$conf.printer-cycle.bak" 2>/dev/null || true
	mv "$tmp" "$conf"
	say "mDNS name resolution configured in $conf"
}

# ---------------------------------------------------------------------------
# The account, the directories, and the binaries
# ---------------------------------------------------------------------------

# SERVICE_USER is who core and the dashboard run as.
#
# Its own account rather than root. Core talks to a socket on the network and
# hands documents to a filter chain, and neither of those needs the ability to
# do anything else on the machine.
SERVICE_USER=printer-cycle
CONFIG_DIR=/etc/printer-cycle
DATA_DIR=/var/lib/printer-cycle
BIN_DIR=/usr/local/bin

# cups_admin_group is the group CUPS treats as its administrators.
#
# Read from CUPS rather than assumed, because it is configuration and it
# differs: cups-files.conf names it in SystemGroup, and distributions disagree
# about whether that is lpadmin, sys, or wheel.
cups_admin_group() {
	group=$(sed -n 's/^SystemGroup[[:space:]]*//p' /etc/cups/cups-files.conf 2>/dev/null |
		head -1 | tr ' ' '\n' | grep -v '^root$' | head -1)
	[ -n "$group" ] || group=lpadmin
	echo "$group"
}

# create_user makes the service account and puts it in the CUPS admin group.
#
# The group membership is needed for two separate things, and the second is not
# obvious. Administrative operations need it, which is expected. But CUPS also
# blanks job-name and job-originating-user-name for any client it does not treat
# as an owner or a system user, silently, under JobPrivateValues. A core outside
# that group therefore lists every job with no name and no owner, and nothing
# anywhere reports an error. That cost a stage to find.
create_user() {
	admin_group=$(cups_admin_group)

	if id "$SERVICE_USER" >/dev/null 2>&1; then
		say "the $SERVICE_USER account already exists"
	elif command -v useradd >/dev/null 2>&1; then
		useradd --system --home-dir "$DATA_DIR" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER"
	elif command -v adduser >/dev/null 2>&1; then
		# busybox adduser, on Alpine. Different flags, and one difference that
		# is not about flags at all: useradd --system creates a group of the
		# same name and busybox adduser does not, so it has to be made first.
		# Without it OpenRC refuses to start the service with
		# "group `printer-cycle` not found", which is a clearer error than most
		# and still took a container to see.
		addgroup -S "$SERVICE_USER" 2>/dev/null || true
		adduser -S -D -H -h "$DATA_DIR" -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
	else
		die "no useradd or adduser on this machine, so the $SERVICE_USER account cannot be created"
	fi

	if ! getent group "$admin_group" >/dev/null 2>&1; then
		warn "there is no $admin_group group, so core may not be able to administer CUPS"
		return 0
	fi

	if id -nG "$SERVICE_USER" 2>/dev/null | tr ' ' '\n' | grep -qx "$admin_group"; then
		say "$SERVICE_USER is already in $admin_group"
		return 0
	fi

	if command -v usermod >/dev/null 2>&1; then
		usermod -aG "$admin_group" "$SERVICE_USER"
	elif command -v addgroup >/dev/null 2>&1; then
		addgroup "$SERVICE_USER" "$admin_group"
	else
		die "cannot add $SERVICE_USER to $admin_group: no usermod or addgroup"
	fi
	say "$SERVICE_USER added to $admin_group"
}

# create_directories makes the two places printer-cycle keeps things.
#
# The data directory holds the database, which holds password hashes, session
# tokens and connector public keys, so it is readable by nobody else. The
# config directory is owned by root and merely readable by the service, because
# a service that can rewrite its own configuration is a service that can grant
# itself things.
create_directories() {
	mkdir -p "$CONFIG_DIR" "$DATA_DIR"

	chown root:root "$CONFIG_DIR"
	chmod 0755 "$CONFIG_DIR"

	chown "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR" 2>/dev/null ||
		chown "$SERVICE_USER" "$DATA_DIR"
	chmod 0700 "$DATA_DIR"

	say "$CONFIG_DIR and $DATA_DIR ready"
}

# checksum prints the sha256 of a file, using whatever this machine has.
checksum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= //'
	else
		die "no sha256sum, shasum or openssl on this machine, so downloads cannot be verified. printer-cycle will not install unverified binaries."
	fi
}

# fetch downloads a URL to a path.
fetch() {
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 2 -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$dest" "$url"
	else
		die "no curl or wget on this machine, so nothing can be downloaded"
	fi
}

# install_binaries puts core and the dashboard on the machine.
#
# Two ways in. --from takes them from a directory, which is what a build has and
# what an air-gapped install needs. Otherwise they are downloaded and checked
# against a SHA256SUMS file published beside them.
#
# An unverified binary is never installed. Not because a mirror is likely to be
# hostile, but because "it downloaded something and ran it as a system service"
# is not a sentence anybody should have to read about their own machine.
install_binaries() {
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT

	for binary in printer-cycle-core printer-cycle-dashboard; do
		name="$binary-linux-$arch"

		if [ -n "$FROM_DIR" ]; then
			[ -f "$FROM_DIR/$name" ] || die "$FROM_DIR/$name is not there"
			cp "$FROM_DIR/$name" "$work/$name"
		else
			url="$RELEASE_URL/$name"
			say "downloading $name"
			fetch "$url" "$work/$name" || die "could not download $url"
		fi
	done

	if [ -n "$FROM_DIR" ]; then
		say "installing binaries from $FROM_DIR without checking them, as asked"
	else
		say "verifying"
		fetch "$RELEASE_URL/SHA256SUMS" "$work/SHA256SUMS" ||
			die "could not download $RELEASE_URL/SHA256SUMS, and printer-cycle will not install binaries it cannot check"

		for binary in printer-cycle-core printer-cycle-dashboard; do
			name="$binary-linux-$arch"
			want=$(grep "  $name\$" "$work/SHA256SUMS" 2>/dev/null | cut -d' ' -f1 | head -1)
			[ -n "$want" ] ||
				die "SHA256SUMS does not mention $name, so it cannot be checked"

			got=$(checksum "$work/$name")
			if [ "$got" != "$want" ]; then
				die "$name does not match its checksum. Expected $want, got $got. Nothing has been installed."
			fi
		done
		say "checksums match"
	fi

	for binary in printer-cycle-core printer-cycle-dashboard; do
		name="$binary-linux-$arch"
		install -m 0755 -o root -g root "$work/$name" "$BIN_DIR/$binary"
	done

	rm -rf "$work"
	trap - EXIT
	say "installed $BIN_DIR/printer-cycle-core and $BIN_DIR/printer-cycle-dashboard"
}

# ---------------------------------------------------------------------------
# Making it start on its own
#
# The units are written here rather than shipped beside this script, because the
# documented way to install is to pipe this one file into sh. An installer that
# needs to fetch three more files is an installer that fails differently on a
# machine with a captive portal.
# ---------------------------------------------------------------------------

# init_system says what will be starting things.
#
# Asked of the machine rather than assumed from the distribution: Debian with
# systemd removed is a real thing, and so is Alpine with systemd added.
init_system() {
	if [ -d /run/systemd/system ]; then
		echo systemd
	elif command -v rc-update >/dev/null 2>&1; then
		echo openrc
	elif command -v systemctl >/dev/null 2>&1; then
		# systemd is installed but not running, which is what a container looks
		# like. The units are still worth writing: the machine they end up on
		# will boot with it.
		echo systemd-inactive
	else
		echo none
	fi
}

write_systemd_units() {
	cat >/etc/systemd/system/printer-cycle-core.service <<UNIT
[Unit]
Description=printer-cycle core
Documentation=https://github.com/mhd64real/printer-cycle
# CUPS is what core talks to, and it talks over cups.socket, so the socket
# rather than the service: systemd starts cupsd on first connection.
After=network.target cups.socket
Wants=cups.socket

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
# The group CUPS treats as administrators. Without it core cannot add a printer,
# and CUPS silently blanks the name and owner of every job it lists.
SupplementaryGroups=$(cups_admin_group)
ExecStart=$BIN_DIR/printer-cycle-core
EnvironmentFile=-$CONFIG_DIR/core.env
Restart=on-failure
RestartSec=5s

# A print server has no business doing most of what a process may do.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
# The database, and the firmware directory, which core writes into when a
# printer needs a file it has to fetch.
ReadWritePaths=$DATA_DIR /lib/firmware/hp

[Install]
WantedBy=multi-user.target
UNIT

	cat >/etc/systemd/system/printer-cycle-dashboard.service <<UNIT
[Unit]
Description=printer-cycle dashboard
Documentation=https://github.com/mhd64real/printer-cycle
After=printer-cycle-core.service
# Wants rather than Requires: the dashboard reconnects on its own, and core
# restarting should not take the interface down with it.
Wants=printer-cycle-core.service

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
ExecStart=$BIN_DIR/printer-cycle-dashboard --setup-token $DATA_DIR/setup-token
EnvironmentFile=-$CONFIG_DIR/dashboard.env
Restart=on-failure
RestartSec=5s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
ReadWritePaths=$DATA_DIR

[Install]
WantedBy=multi-user.target
UNIT

	say "wrote systemd units"
}

write_openrc_services() {
	# supervise-daemon rather than start-stop-daemon, because it is the one
	# that notices a process dying and starts it again. start-stop-daemon
	# launches and forgets.
	cat >/etc/init.d/printer-cycle-core <<'UNIT'
#!/sbin/openrc-run

name="printer-cycle core"
description="printer-cycle core"

command="/usr/local/bin/printer-cycle-core"
# The user only, not user:group. OpenRC passes a "user:group" through to
# supervise-daemon as two arguments rather than as --user and --group, and the
# child then dies on exec leaving a defunct process, while the service reports
# itself started. The account's primary group is already the right one, so
# naming it bought nothing and cost that.
command_user="printer-cycle"
supervisor="supervise-daemon"
respawn_delay=5
respawn_max=0
output_log="/var/log/printer-cycle-core.log"
error_log="/var/log/printer-cycle-core.log"

depend() {
	need net
	after cupsd
}
UNIT

	cat >/etc/init.d/printer-cycle-dashboard <<'UNIT'
#!/sbin/openrc-run

name="printer-cycle dashboard"
description="printer-cycle dashboard"

command="/usr/local/bin/printer-cycle-dashboard"
command_args="--setup-token /var/lib/printer-cycle/setup-token"
command_user="printer-cycle"
supervisor="supervise-daemon"
respawn_delay=5
respawn_max=0
output_log="/var/log/printer-cycle-dashboard.log"
error_log="/var/log/printer-cycle-dashboard.log"

depend() {
	need net
	after printer-cycle-core
}
UNIT

	chmod 0755 /etc/init.d/printer-cycle-core /etc/init.d/printer-cycle-dashboard

	# The log files have to exist and be owned by the service account before
	# anything starts.
	#
	# supervise-daemon opens the redirection after dropping privileges, and
	# /var/log is root-owned, so the child cannot create its own log and dies on
	# the spot. What that looks like from outside is the worst possible thing:
	# the supervisor is running, the service reports "started", and nothing is
	# serving. There is no error anywhere, because the process that would have
	# written it is the one that could not open the file.
	for log in /var/log/printer-cycle-core.log /var/log/printer-cycle-dashboard.log; do
		touch "$log"
		chown "$SERVICE_USER" "$log" 2>/dev/null || true
		chmod 0640 "$log"
	done

	say "wrote OpenRC services"
}

install_services() {
	init=$(init_system)
	case "$init" in
	systemd | systemd-inactive)
		write_systemd_units
		if [ "$init" = systemd ]; then
			systemctl daemon-reload
			systemctl enable --now printer-cycle-core.service printer-cycle-dashboard.service
			say "core and the dashboard are running, and will start on boot"
		else
			say "systemd is installed but not running here, so the units were written and not started"
		fi
		;;
	openrc)
		write_openrc_services
		rc-update add printer-cycle-core default >/dev/null 2>&1 || true
		rc-update add printer-cycle-dashboard default >/dev/null 2>&1 || true
		rc-service printer-cycle-core start >/dev/null 2>&1 || true
		rc-service printer-cycle-dashboard start >/dev/null 2>&1 || true
		say "core and the dashboard added to the default runlevel"
		;;
	none)
		warn "this machine has neither systemd nor OpenRC, so nothing was set up to start on boot. Run $BIN_DIR/printer-cycle-core and $BIN_DIR/printer-cycle-dashboard yourself."
		;;
	esac
}

do_install() {
	require_root

	arch=$(detect_arch)
	family=$(detect_family)
	manager=$(detect_manager "$family")

	command -v "$manager" >/dev/null 2>&1 ||
		die "$manager is not on this machine, so packages cannot be installed"

	say "installing printer-cycle for $family on $arch"

	for limit in $(limitations "$family"); do
		warn "$(describe_limitation "$limit")"
	done

	refresh_index "$manager"
	install_packages "$manager" $(packages_core "$family")

	if [ "$MINIMAL" = yes ]; then
		say "skipping the driver set, as asked"
	else
		# Separate from the core packages on purpose. The driver set is large
		# and the slow part of any install, and a failure here leaves a working
		# printing system that can still drive a modern printer, which is worth
		# distinguishing from a failure to install CUPS at all.
		drivers=$(packages_drivers "$family")
		if [ "$family" = debian ]; then
			drivers=$(echo "$drivers" | sed "s/printer-driver-all/$(expand_drivers | tr '\n' ' ')/")
		fi

		install_packages "$manager" $drivers ||
			warn "the driver set did not install completely. Printers needing no driver will still work."
	fi

	configure_mdns "$family"

	create_user
	create_directories
	install_binaries
	install_services

	say "done"
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

  sh install.sh              install the printing system, every driver, and printer-cycle
  sh install.sh --minimal    install without the driver set
  sh install.sh --from DIR   install binaries from a directory rather than downloading
  sh install.sh --detect     report what this machine is, and change nothing

EOF
}

MINIMAL=no
FROM_DIR=""

# RELEASE_URL is where the binaries come from. Overridable so an install can be
# pointed at a mirror, and so this can be tested without publishing anything.
RELEASE_URL=${PRINTER_CYCLE_RELEASE_URL:-https://github.com/mhd64real/printer-cycle/releases/latest/download}

main() {
	action=install
	while [ $# -gt 0 ]; do
		case "$1" in
		--detect) action=detect ;;
		--minimal) MINIMAL=yes ;;
		--from)
			shift
			[ $# -gt 0 ] || die "--from needs a directory"
			FROM_DIR=$1
			;;
		-h | --help)
			usage
			return 0
			;;
		*)
			usage
			die "unknown option $1"
			;;
		esac
		shift
	done

	case "$action" in
	detect) detect_report ;;
	install) do_install ;;
	esac
}

main "$@"
