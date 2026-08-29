#!/bin/sh
# The CUPS container: D-Bus, Avahi, then cupsd in the foreground.
set -e

mkdir -p /run/dbus && rm -f /run/dbus/pid
dbus-daemon --system --fork

# Avahi, so the CUPS dnssd backend has something to ask. Without it the backend
# exits with "Unable to create Avahi client" and network discovery finds nothing.
avahi-daemon --daemonize --no-drop-root

exec /usr/sbin/cupsd -f
