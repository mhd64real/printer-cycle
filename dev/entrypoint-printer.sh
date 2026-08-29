#!/bin/sh
# A virtual IPP Everywhere printer, in its own container on purpose.
#
# It has to be a separate container rather than another process next to cupsd,
# because the CUPS dnssd backend deliberately ignores services advertised by the
# local machine. Running it here gives it its own network namespace and its own
# IP, so cupsd sees it exactly the way it would see a real printer on the LAN.
set -e

mkdir -p /run/dbus && rm -f /run/dbus/pid
dbus-daemon --system --fork

avahi-daemon --daemonize --no-drop-root

exec ippeveprinter -f application/pdf -p 8632 "Virtual Office Printer"
