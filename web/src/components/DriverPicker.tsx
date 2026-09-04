import { useEffect, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Notice } from "@/components/Notice";
import { api, type DriverCandidate } from "@/api";

/**
 * Choosing a driver by hand, for a printer that could not say what it is.
 *
 * This is the old printer the project is for: a JetDirect box found by SNMP
 * with no device id, which nothing can identify automatically. Every other path
 * through the interface is one click, and this one cannot be, so the job here
 * is to make a catalogue of eighteen thousand drivers navigable rather than to
 * pretend it is small.
 *
 * Manufacturer first, then model, because that is the order somebody reads it
 * off the front of the machine.
 */
export function DriverPicker({
  onChoose,
  onCancel,
  busy,
}: {
  /** Both, because the model chosen here is usually the best name for it. */
  onChoose: (driver: DriverCandidate) => void;
  onCancel: () => void;
  busy?: boolean;
}) {
  const [makes, setMakes] = useState<string[] | null>(null);
  const [make, setMake] = useState("");
  const [query, setQuery] = useState("");

  const [drivers, setDrivers] = useState<DriverCandidate[] | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .driverMakes()
      .then(({ makes }) => setMakes(makes ?? []))
      .catch((err) =>
        setError(err instanceof Error ? err.message : "cannot list the manufacturers"),
      );
  }, []);

  async function search(event: FormEvent) {
    event.preventDefault();
    if (!make && !query.trim()) return;

    setSearching(true);
    setError(null);
    try {
      const found = await api.drivers({
        make: make || undefined,
        query: query.trim() || undefined,
        limit: 200,
      });
      setDrivers(found.drivers ?? []);
      setTruncated(found.truncated);
    } catch (err) {
      setError(err instanceof Error ? err.message : "cannot search for drivers");
    } finally {
      setSearching(false);
    }
  }

  return (
    <div className="mt-3 space-y-3">
      <p className="text-sm text-muted">
        This printer did not say what model it is, so its driver has to be chosen. Pick the
        make, then find the model.
      </p>

      <form onSubmit={search} className="space-y-3">
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-1.5">
            <label htmlFor="driver-make" className="block text-sm font-medium">
              Make
            </label>
            <select
              id="driver-make"
              value={make}
              onChange={(e) => setMake(e.target.value)}
              disabled={makes === null || busy}
              className="rounded-md border border-line bg-raised px-3 py-2 text-ink
                         disabled:opacity-60"
            >
              <option value="">{makes === null ? "Loading" : "Any"}</option>
              {(makes ?? []).map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </div>

          <div className="min-w-40 flex-1 space-y-1.5">
            <label htmlFor="driver-query" className="block text-sm font-medium">
              Model
            </label>
            <input
              id="driver-query"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="LaserJet 4"
              disabled={busy}
              className="w-full rounded-md border border-line bg-raised px-3 py-2
                         text-ink placeholder:text-muted disabled:opacity-60"
            />
          </div>

          <Button type="submit" disabled={searching || busy || (!make && !query.trim())}>
            {searching ? "Looking" : "Find"}
          </Button>
          <Button type="button" variant="plain" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        </div>
      </form>

      {error ? <Notice>{error}</Notice> : null}

      {drivers?.length === 0 ? (
        <p className="text-sm text-muted">
          Nothing matched. Try the make on its own, or a shorter model name.
        </p>
      ) : null}

      {drivers && drivers.length > 0 ? (
        <div>
          {/*
            Bounded and scrollable rather than however long it happens to be. A
            manufacturer can have thousands of drivers, and a list that pushes
            the rest of the page out of reach is not a list somebody can use.
          */}
          <ul className="max-h-72 divide-y divide-line overflow-y-auto rounded-md border border-line">
            {drivers.map((driver) => (
              <li
                key={driver.ppd}
                className="flex items-center justify-between gap-3 px-3 py-2"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm">{driver.make_and_model}</p>
                  {driver.requires_proprietary_plugin ? (
                    <p className="text-sm text-muted">Needs a closed vendor plugin</p>
                  ) : null}
                </div>
                <Button onClick={() => onChoose(driver)} disabled={busy}>
                  {driver.recommended ? "Use (recommended)" : "Use"}
                </Button>
              </li>
            ))}
          </ul>
          {truncated ? (
            <p className="mt-2 text-sm text-muted">
              Only the first {drivers.length} are shown. Narrow the model to see the rest.
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
