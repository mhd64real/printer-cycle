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
      libnss-mdns \
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

# Resolve .local names, not merely discover them.
#
# Avahi lets CUPS find a printer over mDNS; resolving the .local hostname it
# comes back with is a separate thing, done by the name service switch. Without
# this, discovery works, pairing gets as far as CUPS trying to reach the printer,
# and fails with "Temporary failure in name resolution" several seconds later.
#
# A desktop Linux install has this by default. A container does not, and neither
# does a minimal server install, which is what a Raspberry Pi usually is: the
# installer has to add it too.
RUN sed -i "s/^hosts:.*/hosts: files mdns4_minimal [NOTFOUND=return] dns mdns4/" /etc/nsswitch.conf

COPY cupsd.conf /etc/cups/cupsd.conf

COPY entrypoint-cups.sh entrypoint-printer.sh create-printers.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/entrypoint-cups.sh \
             /usr/local/bin/entrypoint-printer.sh \
             /usr/local/bin/create-printers.sh

EXPOSE 631

CMD ["/usr/local/bin/entrypoint-cups.sh"]
