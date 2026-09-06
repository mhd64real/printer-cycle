import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { DriverPicker } from "@/components/DriverPicker";
import {
  ApiError,
  DRIVER_REQUIRED,
  api,
  subscribe,
  type Device,
  type DriverCandidate,
  type Printer,
} from "@/api";

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

  // A full device URI is the expert path: it skips the lookup, because somebody
  // who knows exactly what they want should not be made to sit through a guess
  // at it. It still produces a row to confirm rather than a queue outright.
  //
  // That confirmation was added when adding by hand grew a driver step. A
  // printer that cannot say what model it is has to be given a driver, and the
  // choosing happens on the row. Two paths to the same row beats two copies of
  // the same fallback, and seeing what is about to be added is not a cost.
  const looksLikeURI = address.includes("://");

  async function look(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setFound(null);
    setBusy(true);

    try {
      if (looksLikeURI) {
        const uri = address.trim();
        setFound({
          // A pasted uri describes no hardware, so the uri is the only truthful
          // name for it. If a driver is chosen for it, that driver's model
          // replaces this.
          name: uri,
          device_uri: uri,
          device_id: "",
          make_and_model: "",
          info: "",
          location: "",
          transport: uri.split("://")[0] ?? "",
        });
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
            {busy ? "Looking" : looksLikeURI ? "Use it" : "Look for it"}
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
  const [choosingDriver, setChoosingDriver] = useState(false);

  const [fetching, setFetching] = useState(false);
  // Seeded from what core said, so a reload does not offer to fetch a file that
  // is already there.
  const [firmwareDone, setFirmwareDone] = useState(!!device.firmware_installed);
  const [firmwareError, setFirmwareError] = useState<string | null>(null);

  const name = device.name;

  async function getFirmware() {
    setFetching(true);
    setFirmwareError(null);
    try {
      await api.installFirmware(device.device_id);
      setFirmwareDone(true);
    } catch (err) {
      setFirmwareError(
        err instanceof Error ? err.message : "the firmware could not be fetched",
      );
    } finally {
      setFetching(false);
    }
  }

  async function pair(driver?: DriverCandidate) {
    setBusy(true);
    setError(null);
    try {
      await api.addPrinter({
        deviceUri: device.device_uri,
        // A printer that could not say what it is has no name worth using: the
        // fallback is its device uri, so a queue ends up called
        // "socket://192.0.2.10:9100". Choosing a driver is the moment its model
        // becomes known, so that is what it gets called.
        name: device.make_and_model
          ? name
          : driver
            ? (driver.name ?? driver.make_and_model)
            : name,
        deviceId: device.device_id,
        ...(driver ? { ppd: driver.ppd } : {}),
      });
      onAdded();
    } catch (err) {
      // Core refusing for want of a driver is not a dead end, it is the next
      // step. Recognised by code rather than by message, so improving the
      // wording cannot quietly turn it back into a dead end.
      if (err instanceof ApiError && err.code === DRIVER_REQUIRED) {
        setChoosingDriver(true);
        setBusy(false);
        return;
      }
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
          {device.needs_firmware ? (
            <div className="mt-1">
              {/*
                Said before pairing rather than after, because this is the
                difference between a printer that works and one that sits there
                printing nothing while reporting no error at all. Deliberately
                not in the same words as a driver needing a proprietary plugin:
                that one is a wall, this one is a wait.
              */}
              <p className="text-sm text-muted">
                This model keeps no firmware of its own and loads it from this
                machine every time it is switched on. The file cannot be shipped
                with Linux, so it has to be fetched once before the printer will
                print anything.
              </p>
              {firmwareDone ? (
                <p className="mt-1 text-sm text-muted">The firmware is here now.</p>
              ) : (
                <Button
                  variant="plain"
                  className="mt-2"
                  onClick={getFirmware}
                  disabled={fetching}
                >
                  {fetching ? "Fetching" : "Fetch the firmware"}
                </Button>
              )}
              {firmwareError ? (
                <div className="mt-2">
                  {/*
                    Core's own words, not a summary of them. Every message the
                    firmware fetcher produces is written for whoever is looking
                    at this screen, and it is the difference between "offline"
                    and "the mirror moved".
                  */}
                  <Notice>{firmwareError}</Notice>
                </div>
              ) : null}
            </div>
          ) : null}
        </div>
        <Button onClick={() => pair()} disabled={busy || choosingDriver}>
          {busy ? "Adding" : "Add"}
        </Button>
      </div>
      {busy ? (
        <p className="mt-2 text-sm text-muted">
          Working out which driver this printer needs. This can take a few
          seconds.
        </p>
      ) : null}

      {choosingDriver ? (
        <DriverPicker
          busy={busy}
          onChoose={(driver) => pair(driver)}
          onCancel={() => setChoosingDriver(false)}
        />
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
