import { useCallback, useEffect, useState } from "react";

import { Button } from "@/components/Button";
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
        setDevices((current) =>
          current.some((d) => d.device_uri === device.device_uri) ? current : [...current, device],
        );
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
    </div>
  );
}

function DeviceRow({ device, onAdded }: { device: Device; onAdded: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const name = device.make_and_model || device.info || device.device_uri;

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
            {describeTransport(device.transport)}
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
