import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { api, subscribe, type Device, type Printer } from "@/api";

/**
 * The printers page.
 *
 * The screen the whole product exists for: something that was difficult becomes
 * ordinary, in one click, without anybody being shown a list of eighteen
 * thousand drivers.
 */
export function Printers() {
  const [printers, setPrinters] = useState<Printer[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const { printers } = await api.printers();
      setPrinters(printers ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "cannot list printers");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <section className="space-y-6">
      <header className="flex items-center justify-between gap-4">
        <h2 className="text-lg font-medium">Printers</h2>
        {!adding ? <Button onClick={() => setAdding(true)}>Add a printer</Button> : null}
      </header>

      {error ? <Notice>{error}</Notice> : null}

      {adding ? (
        <AddPrinter
          onDone={() => {
            setAdding(false);
            void refresh();
          }}
          onCancel={() => setAdding(false)}
        />
      ) : null}

      {printers === null ? (
        <p className="text-muted">Loading.</p>
      ) : printers.length === 0 ? (
        <p className="text-muted">
          No printers yet. Add one and printer-cycle will work out which driver it needs.
        </p>
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {printers.map((printer) => (
            <PrinterRow key={printer.id} printer={printer} onRemoved={refresh} />
          ))}
        </ul>
      )}
    </section>
  );
}

function PrinterRow({ printer, onRemoved }: { printer: Printer; onRemoved: () => void }) {
  const [busy, setBusy] = useState(false);

  async function remove() {
    setBusy(true);
    await api.removePrinter(printer.id).catch(() => undefined);
    onRemoved();
  }

  return (
    <li className="flex items-center justify-between gap-4 py-4">
      <div className="min-w-0">
        <p className="truncate font-medium">{printer.name}</p>
        <p className="truncate text-sm text-muted">
          {printer.location ? `${printer.location}, ` : ""}
          {printer.device_uri}
        </p>
      </div>
      <Button variant="plain" onClick={remove} disabled={busy}>
        {busy ? "Removing" : "Remove"}
      </Button>
    </li>
  );
}

function AddPrinter({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [searching, setSearching] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Devices arrive as they are found rather than all at the end. Discovery
    // takes seconds, because finding an old network printer means waiting out an
    // SNMP broadcast, and a page that showed nothing until then would look
    // broken rather than thorough.
    const stop = subscribe({
      "printer.discovered": (data) => {
        const device = data as Device;
        setDevices((current) => {
          // Replace in place rather than append, and hold the position: a
          // printer that jumped down the list the moment a better description
          // of it arrived would move under a cursor already on its way to it.
          const at = current.findIndex((d) => sameDevice(d, device));
          if (at === -1) return [...current, device];
          return current.map((d, i) => (i === at ? device : d));
        });
      },
    });

    api
      .discover()
      .then(({ devices }) => setDevices(devices ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "cannot search for printers"))
      .finally(() => setSearching(false));

    return stop;
  }, []);

  return (
    <div className="rounded-md border border-line bg-raised p-4">
      <div className="flex items-center justify-between gap-4">
        <h3 className="font-medium">
          {searching ? "Looking for printers" : `Found ${devices.length}`}
        </h3>
        <Button variant="plain" onClick={onCancel}>
          Cancel
        </Button>
      </div>

      <p className="mt-1 text-sm text-muted">
        Anything plugged in or on this network. Searching takes a few seconds
        because older printers answer slowly.
      </p>

      {error ? (
        <div className="mt-4">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      {devices.length === 0 ? (
        <p className="mt-4 text-sm text-muted">
          {searching ? "Searching." : "Nothing found. A printer may need to be switched on."}
        </p>
      ) : (
        <ul className="mt-4 divide-y divide-line">
          {devices.map((device) => (
            <DeviceRow key={device.device_uri} device={device} onAdded={onDone} />
          ))}
        </ul>
      )}

      <ByAddress onAdded={onDone} searching={searching} />
    </div>
  );
}

/**
 * Adding a printer by typing where it is.
 *
 * Searching finds printers that announce themselves. Plenty do not: one on
 * another subnet, one with mDNS switched off, a network that filters broadcast
 * traffic, or simply an older printer that was never going to say anything. All
 * somebody should need to know is the address.
 */
function ByAddress({ onAdded, searching }: { onAdded: () => void; searching: boolean }) {
  const [open, setOpen] = useState(false);
  const [address, setAddress] = useState("");
  const [found, setFound] = useState<(Device & { port?: number }) | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A full device URI is the expert path: somebody who knows exactly what they
  // want should not be made to go through a lookup that guesses at it.
  const looksLikeURI = address.includes("://");

  async function look(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setFound(null);
    setBusy(true);

    try {
      if (looksLikeURI) {
        await api.addPrinter({ deviceUri: address.trim(), name: address.trim() });
        onAdded();
        return;
      }
      setFound(await api.probe(address.trim()));
    } catch (err) {
      setError(err instanceof Error ? err.message : "nothing answered at that address");
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <p className="mt-4 border-t border-line pt-4 text-sm text-muted">
        {searching ? "Still looking. " : ""}
        Not listed?{" "}
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="text-accent underline underline-offset-2"
        >
          Add it by address
        </button>
        .
      </p>
    );
  }

  return (
    <div className="mt-4 border-t border-line pt-4">
      <form onSubmit={look} className="space-y-3">
        <Field
          label="Printer address"
          hint="An address like 192.168.1.50, or printer.local. A full device URI works too."
          placeholder="192.168.1.50"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          autoFocus
          disabled={busy}
        />
        <div className="flex gap-2">
          <Button type="submit" disabled={busy || address.trim() === ""}>
            {busy ? (looksLikeURI ? "Adding" : "Looking") : looksLikeURI ? "Add" : "Look for it"}
          </Button>
          <Button type="button" variant="plain" onClick={() => setOpen(false)} disabled={busy}>
            Back
          </Button>
        </div>
      </form>

      {error ? (
        <div className="mt-3">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      {found ? (
        <div className="mt-3">
          <p className="text-sm text-muted">Answered at that address</p>
          <ul className="divide-y divide-line">
            {/*
              Deliberately labelled by address rather than by how it is
              attached. The list above already says "On this network" for every
              row, so repeating it here made a probe result look like a second
              copy of a printer that was already listed. What the user wants
              confirmed is which machine answered where they pointed.
            */}
            <DeviceRow device={found} onAdded={onAdded} subtitle={found.device_uri} />
          </ul>
        </div>
      ) : null}
    </div>
  );
}

function DeviceRow({
  device,
  onAdded,
  subtitle,
}: {
  device: Device;
  onAdded: () => void;
  subtitle?: string;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const name = displayName(device);

  async function pair() {
    setBusy(true);
    setError(null);
    try {
      await api.addPrinter({
        deviceUri: device.device_uri,
        name,
        deviceId: device.device_id,
      });
      onAdded();
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not add this printer");
      setBusy(false);
    }
  }

  return (
    <li className="py-3">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <p className="truncate font-medium">{name}</p>
          <p className="truncate text-sm text-muted">
            {subtitle ?? describeTransport(device.transport)}
            {device.make_and_model ? "" : ", model unknown"}
          </p>
        </div>
        <Button onClick={pair} disabled={busy}>
          {busy ? "Adding" : "Add"}
        </Button>
      </div>
      {busy ? (
        <p className="mt-2 text-sm text-muted">
          Working out which driver this printer needs. This can take a few
          seconds.
        </p>
      ) : null}

      {error ? (
        <div className="mt-2">
          <Notice>{error}</Notice>
        </div>
      ) : null}
    </li>
  );
}

/** Whether two announcements describe one printer. */
function sameDevice(a: Device, b: Device): boolean {
  if (a.identity && b.identity) return a.identity === b.identity;
  return a.device_uri === b.device_uri;
}

/**
 * The name to show for a printer, without the manufacturer said twice.
 *
 * CUPS builds make-and-model by putting the manufacturer in front of the model,
 * and most printers already put it in the model themselves, so the raw string
 * comes back as "HP HP LaserJet 4000" or "Brother Brother HL-2270DW". Vendor
 * software tends to just print that. Showing it that way here would undercut
 * the whole point of this project, and the string also becomes the default
 * queue name, so the stutter would follow the printer around.
 *
 * Only a leading repeat is collapsed. A model that genuinely repeats a word
 * later on keeps it.
 */
export function displayName(device: Device): string {
  const raw = (device.make_and_model || device.info || device.device_uri).trim();
  const [first, second, ...rest] = raw.split(/\s+/);
  if (first && second && first.toLowerCase() === second.toLowerCase()) {
    return [second, ...rest].join(" ");
  }
  return raw;
}

/** Says how a printer is attached, in words rather than a URI scheme. */
function describeTransport(transport: string): string {
  switch (transport) {
    case "usb":
      return "Plugged in over USB";
    case "dnssd":
    case "ipp":
    case "ipps":
      return "On this network";
    case "socket":
      return "On this network, older style";
    case "lpd":
      return "On this network, LPD";
    default:
      return transport || "Unknown connection";
  }
}
