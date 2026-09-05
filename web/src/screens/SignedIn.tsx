import { useState } from "react";

import { Button } from "@/components/Button";
import { Connectors } from "@/screens/Connectors";
import { Jobs } from "@/screens/Jobs";
import { Links } from "@/screens/Links";
import { Print } from "@/screens/Print";
import { Printers } from "@/screens/Printers";
import { api, type User } from "@/api";

const TABS = [
  { key: "print", label: "Print" },
  { key: "jobs", label: "Jobs" },
  { key: "printers", label: "Printers" },
  { key: "connectors", label: "Connectors" },
  { key: "links", label: "Linked accounts" },
] as const;

type Tab = (typeof TABS)[number]["key"];

export function SignedIn({ user, onSignOut }: { user: User; onSignOut: () => void }) {
  const [tab, setTab] = useState<Tab>("print");

  async function signOut() {
    await api.signOut().catch(() => undefined);
    onSignOut();
  }

  return (
    <div className="mx-auto max-w-3xl px-6 py-10">
      <header className="flex flex-wrap items-baseline justify-between gap-4 border-b border-line pb-4">
        <h1 className="text-2xl font-semibold tracking-tight">printer-cycle</h1>
        <div className="flex items-center gap-3 text-sm">
          <span className="text-muted">
            {user.display_name || user.username}
            {user.is_admin ? " (administrator)" : ""}
          </span>
          <Button variant="plain" onClick={signOut}>
            Sign out
          </Button>
        </div>
      </header>

      <nav className="mt-4 flex gap-1 border-b border-line">
        {TABS.map((entry) => (
          <button
            key={entry.key}
            type="button"
            onClick={() => setTab(entry.key)}
            aria-current={tab === entry.key ? "page" : undefined}
            className={
              tab === entry.key
                ? "-mb-px border-b-2 border-accent px-3 py-2 text-sm font-medium text-ink"
                : "-mb-px border-b-2 border-transparent px-3 py-2 text-sm text-muted hover:text-ink"
            }
          >
            {entry.label}
          </button>
        ))}
      </nav>

      <main className="mt-8">
        {tab === "print" ? (
          <Print />
        ) : tab === "jobs" ? (
          <Jobs />
        ) : tab === "printers" ? (
          <Printers />
        ) : tab === "connectors" ? (
          <Connectors />
        ) : (
          <Links />
        )}
      </main>
    </div>
  );
}
