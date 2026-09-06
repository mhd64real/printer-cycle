#!/bin/sh
#
# Runs install.sh --detect in one container per distribution family, and checks
# every package name it produces against that distribution's own repository.
#
# Slow, and not part of `make check`: it pulls five images and talks to five
# package mirrors. Run it when install.sh changes.
set -eu

cd "$(dirname "$0")/.."

fail=0

check() {
	image=$1
	platform=$2
	query=$3
	shift 3

	printf '\n=== %s ===\n' "$image"

	# --platform is only set for Arch, which publishes no arm64 image. The
	# seccomp option goes with it: pacman drops to a sandbox user whose seccomp
	# filter fails under emulation, which looks exactly like every package
	# being missing.
	set -- docker run --rm ${platform:+--platform "$platform"} \
		${platform:+--security-opt seccomp=unconfined} \
		-v "$PWD/install.sh:/install.sh:ro" "$image" sh -c "$query"

	if ! "$@"; then
		fail=1
	fi
}

report='sh /install.sh --detect'

debian_query="$report && apt-get update -qq >/dev/null 2>&1 && for p in \$(sh /install.sh --detect 2>/dev/null | sed -n 's/^\(core\|driver\)_packages=//p'); do apt-cache show \"\$p\" >/dev/null 2>&1 || { echo \"MISSING \$p\"; exit 1; }; done && echo 'all package names exist'"

rhel_query="$report && for p in \$(sh /install.sh --detect 2>/dev/null | sed -n 's/^\(core\|driver\)_packages=//p'); do dnf -q info \"\$p\" >/dev/null 2>&1 || { echo \"MISSING \$p\"; exit 1; }; done && echo 'all package names exist'"

arch_query="$report && sed -i 's/^DownloadUser/#DownloadUser/' /etc/pacman.conf && pacman -Sy --noconfirm >/dev/null 2>&1 && for p in \$(sh /install.sh --detect 2>/dev/null | sed -n 's/^\(core\|driver\)_packages=//p'); do pacman -Si \"\$p\" >/dev/null 2>&1 || { echo \"MISSING \$p\"; exit 1; }; done && echo 'all package names exist'"

alpine_query="$report && apk update -q >/dev/null 2>&1 && for p in \$(sh /install.sh --detect 2>/dev/null | sed -n 's/^\(core\|driver\)_packages=//p'); do apk search -qx \"\$p\" 2>/dev/null | grep -q . || { echo \"MISSING \$p\"; exit 1; }; done && echo 'all package names exist'"

suse_query="$report && for p in \$(sh /install.sh --detect 2>/dev/null | sed -n 's/^\(core\|driver\)_packages=//p'); do zypper -q --non-interactive info \"\$p\" 2>/dev/null | grep -q '^Name' || { echo \"MISSING \$p\"; exit 1; }; done && echo 'all package names exist'"

check debian:trixie-slim '' "$debian_query"
check fedora:41 '' "$rhel_query"
check archlinux:base linux/amd64 "$arch_query"
check alpine:3.21 '' "$alpine_query"
check opensuse/leap:15.6 '' "$suse_query"

echo
if [ "$fail" -ne 0 ]; then
	echo 'some package names do not exist' >&2
	exit 1
fi
echo 'every family detects, and every package name exists'
