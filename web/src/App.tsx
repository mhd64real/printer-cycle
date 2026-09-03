import { useEffect, useState } from "react";

type Health = {
  version: string;
  core_connected: boolean;
};

/**
 * The whole interface, for now.
 *
 * It exists to prove one thing: that the page is served from inside the binary
 * and can reach the dashboard process behind it. Screens arrive from stage 44.
 */
export default function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    fetch("/healthz")
      .then((r) => r.json())
      .then(setHealth)
      .catch(() => setFailed(true));
  }, []);

  return (
    <main className="mx-auto max-w-xl px-6 py-16">
      <h1 className="text-2xl font-semibold tracking-tight">printer-cycle</h1>

      <p className="mt-2 text-[--color-muted]">
        A print server for old printers, and for printers whose software is
        worse than the hardware.
      </p>

      <dl className="mt-8 divide-y divide-[--color-line] border-y border-[--color-line] text-sm">
        <Row label="Interface">served from inside the binary</Row>
        <Row label="Core">
          {failed
            ? "cannot reach the dashboard process"
            : health
              ? health.core_connected
                ? "connected"
                : "not connected"
              : "checking"}
        </Row>
        <Row label="Version">{health?.version ?? "unknown"}</Row>
      </dl>

      <p className="mt-8 text-sm text-[--color-muted]">
        The screens are not built yet. See PLAN.md, phase 5.
      </p>
    </main>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex justify-between gap-6 py-3">
      <dt className="text-[--color-muted]">{label}</dt>
      <dd className="text-right">{children}</dd>
    </div>
  );
}
