import { useEffect, useRef, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Notice } from "@/components/Notice";
import { api, type Printer } from "@/api";

/**
 * Sizes offered by name.
 *
 * A short list rather than everything IPP defines. These are the sizes people
 * have in a drawer, and anything else is reachable by leaving it alone and
 * letting the printer use its own default, which is what somebody with unusual
 * media has already configured.
 */
const MEDIA = [
  { value: "", label: "Whatever is loaded" },
  { value: "iso_a4_210x297mm", label: "A4" },
  { value: "na_letter_8.5x11in", label: "Letter" },
  { value: "iso_a3_297x420mm", label: "A3" },
  { value: "na_legal_8.5x14in", label: "Legal" },
];

type Sent = { name: string; printer: string };

export function Print() {
  const [printers, setPrinters] = useState<Printer[] | null>(null);
  const [printerId, setPrinterId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [copies, setCopies] = useState(1);
  const [media, setMedia] = useState("");
  const [duplex, setDuplex] = useState<boolean | undefined>(undefined);
  const [color, setColor] = useState<boolean | undefined>(undefined);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<Sent | null>(null);

  const picker = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api
      .printers()
      .then(({ printers }) => {
        setPrinters(printers ?? []);
        // Chosen for them when there is only one, because being asked to pick
        // from a list of one is being asked for nothing.
        if (printers?.length === 1) setPrinterId(printers[0]!.id);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "cannot list printers"));
  }, []);

  async function send(event: FormEvent) {
    event.preventDefault();
    if (!file || !printerId) return;

    setBusy(true);
    setError(null);
    setSent(null);
    try {
      await api.print(printerId, file, {
        copies,
        media: media || undefined,
        duplex,
        color,
      });
      const chosen = printers?.find((p) => p.id === printerId);
      setSent({ name: file.name, printer: chosen?.name ?? "the printer" });
      setFile(null);
      if (picker.current) picker.current.value = "";
    } catch (err) {
      setError(err instanceof Error ? err.message : "that document could not be printed");
    } finally {
      setBusy(false);
    }
  }

  if (printers === null) {
    return <p className="text-sm text-muted">Loading.</p>;
  }

  if (printers.length === 0) {
    return (
      <div>
        <p className="text-sm text-muted">
          No printers yet. Add one on the Printers page and it will show up here.
        </p>
      </div>
    );
  }

  return (
    <div>
      <form onSubmit={send} className="max-w-lg space-y-5">
        <div className="space-y-1.5">
          <label htmlFor="printer" className="block text-sm font-medium">
            Printer
          </label>
          <select
            id="printer"
            value={printerId}
            onChange={(e) => setPrinterId(e.target.value)}
            disabled={busy}
            className="w-full rounded-md border border-line bg-raised px-3 py-2 text-ink
                       disabled:opacity-60"
          >
            <option value="">Choose a printer</option>
            {printers.map((printer) => (
              <option key={printer.id} value={printer.id}>
                {printer.name}
              </option>
            ))}
          </select>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="document" className="block text-sm font-medium">
            Document
          </label>
          <input
            id="document"
            ref={picker}
            type="file"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            disabled={busy}
            className="w-full rounded-md border border-line bg-raised px-3 py-2 text-sm text-ink
                       file:mr-3 file:rounded file:border-0 file:bg-line file:px-3 file:py-1.5
                       file:text-sm file:font-medium file:text-ink
                       disabled:opacity-60"
          />
          <p className="text-sm text-muted">
            PDFs print everywhere. Other formats depend on what the printer accepts, and it
            will say so rather than printing nothing.
          </p>
        </div>

        <div className="flex flex-wrap gap-4">
          <div className="space-y-1.5">
            <label htmlFor="copies" className="block text-sm font-medium">
              Copies
            </label>
            <input
              id="copies"
              type="number"
              min={1}
              max={999}
              value={copies}
              onChange={(e) => setCopies(Math.max(1, Number(e.target.value) || 1))}
              disabled={busy}
              className="w-24 rounded-md border border-line bg-raised px-3 py-2 text-ink
                         disabled:opacity-60"
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="media" className="block text-sm font-medium">
              Paper
            </label>
            <select
              id="media"
              value={media}
              onChange={(e) => setMedia(e.target.value)}
              disabled={busy}
              className="rounded-md border border-line bg-raised px-3 py-2 text-ink
                         disabled:opacity-60"
            >
              {MEDIA.map((size) => (
                <option key={size.value} value={size.value}>
                  {size.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/*
          Three states, not two. "Leave it to the printer" is the default and is
          not the same as off: a printer set up to print double-sided should keep
          doing that for somebody who never touched this control.
        */}
        <Choice label="Sides" value={duplex} onChange={setDuplex} off="One side" on="Both sides" />
        <Choice label="Colour" value={color} onChange={setColor} off="Black and white" on="Colour" />

        <div className="flex items-center gap-3">
          <Button type="submit" disabled={busy || !file || !printerId}>
            {busy ? "Sending" : "Print"}
          </Button>
          {busy ? <span className="text-sm text-muted">Sending the document.</span> : null}
        </div>
      </form>

      {error ? (
        <div className="mt-4 max-w-lg">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      {sent ? (
        <p className="mt-4 max-w-lg text-sm text-muted">
          Sent {sent.name} to {sent.printer}.
        </p>
      ) : null}
    </div>
  );
}

/** A setting with a default that means "do not mention it to the printer". */
function Choice({
  label,
  value,
  onChange,
  off,
  on,
}: {
  label: string;
  value: boolean | undefined;
  onChange: (next: boolean | undefined) => void;
  off: string;
  on: string;
}) {
  const options: { key: string; value: boolean | undefined; label: string }[] = [
    { key: "default", value: undefined, label: "As the printer is set" },
    { key: "off", value: false, label: off },
    { key: "on", value: true, label: on },
  ];

  return (
    <fieldset className="space-y-1.5">
      <legend className="text-sm font-medium">{label}</legend>
      <div className="flex flex-wrap gap-2">
        {options.map((option) => (
          <button
            key={option.key}
            type="button"
            onClick={() => onChange(option.value)}
            aria-pressed={value === option.value}
            className={
              value === option.value
                ? "rounded-md border border-accent bg-accent/15 px-3 py-1.5 text-sm text-ink"
                : "rounded-md border border-line px-3 py-1.5 text-sm text-muted hover:bg-line/40"
            }
          >
            {option.label}
          </button>
        ))}
      </div>
    </fieldset>
  );
}
