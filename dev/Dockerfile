# CUPS for local development.
#
# Debian, because Raspberry Pi OS is Debian and what works here should work
# there. The full open driver set is installed deliberately: it mirrors what the
# install script will put on a real box, so PPD lookups and driver ranking can be
# developed against a realistic catalogue instead of a toy one.
FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
      cups \
      cups-filters \
      cups-ipp-utils \
      ghostscript \
      avahi-daemon \
      avahi-utils \
      dbus \
      foomatic-db-engine \
      foomatic-db-compressed-ppds \
      openprinting-ppds \
      printer-driver-gutenprint \
      printer-driver-hpcups \
      printer-driver-foo2zjs \
      printer-driver-splix \
      printer-driver-brlaser \
      printer-driver-escpr \
      printer-driver-c2esp \
      printer-driver-m2300w \
      printer-driver-pnm2ppa \
      printer-driver-ptouch \
      printer-driver-sag-gdi \
      printer-driver-cjet \
      printer-driver-postscript-hp \
 && rm -rf /var/lib/apt/lists/*

# FileDevice lives in cups-files.conf, not cupsd.conf, since CUPS 1.6. Stage 8
# needs it so queues can write to a file and output can be checked byte for byte.
RUN echo "FileDevice Yes" >> /etc/cups/cups-files.conf

COPY cupsd.conf /etc/cups/cupsd.conf

COPY entrypoint-cups.sh entrypoint-printer.sh create-printers.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/entrypoint-cups.sh \
             /usr/local/bin/entrypoint-printer.sh \
             /usr/local/bin/create-printers.sh

EXPOSE 631

CMD ["/usr/local/bin/entrypoint-cups.sh"]
