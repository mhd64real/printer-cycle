#!/bin/sh
# Creates the virtual queues used for development. Runs inside the CUPS
# container. Idempotent: re-running replaces the queues.
#
# Two queues, both writing to files so output can be checked byte for byte:
#
#   file-ps   Generic PostScript. The easy path, where CUPS passes work through
#             largely untouched.
#   file-pcl  Generic PCL laser. The path that actually matters, because it runs
#             the Ghostscript rasterisation chain, which is what every old
#             printer this project exists for depends on.
set -e

OUT=/var/spool/pc-out
mkdir -p "$OUT"
chmod 1777 "$OUT"

for queue in file-ps file-pcl; do
  lpadmin -x "$queue" 2>/dev/null || true
done

lpadmin -p file-ps -E \
  -v "file://$OUT/file-ps.out" \
  -m drv:///sample.drv/generic.ppd \
  -D "Virtual PostScript printer writing to a file"

lpadmin -p file-pcl -E \
  -v "file://$OUT/file-pcl.out" \
  -m drv:///sample.drv/generpcl.ppd \
  -D "Virtual PCL laser writing to a file, exercises the Ghostscript chain"

for queue in file-ps file-pcl; do
  cupsenable "$queue"
  cupsaccept "$queue"
done

echo "queues created:"
lpstat -p
