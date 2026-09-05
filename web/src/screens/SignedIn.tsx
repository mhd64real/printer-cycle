import { useCallback, useEffect, useState } from "react";

import { Button } from "@/components/Button";
import { Connectors } from "@/screens/Connectors";
import { Jobs } from "@/screens/Jobs";
import { Links } from "@/screens/Links";
import { AddSomeone, People } from "@/screens/People";
import { Print } from "@/screens/Print";
import { Printers } from "@/screens/Printers";
import { api, type User } from "@/api";

const TABS = [
  { key: "print", label: "Print" },
  { key: "jobs", label: "Jobs" },
  { key: "printers", label: "Printers" },
  { key: "connectors", label: "Connectors" },
  { key: "links", label: "Linked accounts" },
  { key: "people", label: "People" },
] as const;

type Tab = (typeof TABS)[number]["key"];

export function SignedIn({ user, onSignOut }: { user: User; onSignOut: () => void }) {
  const [tab, setTab] = useState<Tab>("print");
  const [users, setUsers] = useState<User[] | null>(null);
  const [adding, setAdding] = useState(false);

  const loadUsers = useCallback(async () => {
    try {
      const { users } = await api.users();
      setUsers(users ?? []);
    } catch {
      // Not knowing how many people there are is not worth an error on screen:
      // it decides whether one tab is shown, and printing carries on either way.
      setUsers([]);
    }
  }, []);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  /**
   * User management appears when there is somebody to manage.
   *
   * A household of one has nothing to manage: no roles to assign, no list worth
   * reading, and a page showing a single row with your own name on it is a page
   * that makes somebody wonder what they missed. So it is not there. Adding a
   * second person is what brings it into existence, which is why that one
   * action lives in the header until it does.
   */
  const manyPeople = (users?.length ?? 0) > 1;
  const tabs = TABS.filter((entry) => entry.key !== "people" || manyPeople);

  // Guards against being left looking at a tab that has just stopped existing,
  // which happens the moment a two-person box goes back to one.
  const current = tabs.some((entry) => entry.key === tab) ? tab : "print";

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
          {!manyPeople && user.is_admin ? (
            <Button variant="plain" onClick={() => setAdding(true)}>
              Add someone
            </Button>
          ) : null}
          <Button variant="plain" onClick={signOut}>
            Sign out
          </Button>
        </div>
      </header>

      <nav className="mt-4 flex gap-1 border-b border-line">
        {tabs.map((entry) => (
          <button
            key={entry.key}
            type="button"
            onClick={() => setTab(entry.key)}
            aria-current={current === entry.key ? "page" : undefined}
            className={
              current === entry.key
                ? "-mb-px border-b-2 border-accent px-3 py-2 text-sm font-medium text-ink"
                : "-mb-px border-b-2 border-transparent px-3 py-2 text-sm text-muted hover:text-ink"
            }
          >
            {entry.label}
          </button>
        ))}
      </nav>

      {adding ? (
        <div className="mt-6">
          <AddSomeone
            onAdded={() => {
              setAdding(false);
              void loadUsers();
              setTab("people");
            }}
            onCancel={() => setAdding(false)}
          />
        </div>
      ) : null}

      <main className="mt-8">
        {current === "print" ? (
          <Print />
        ) : current === "jobs" ? (
          <Jobs />
        ) : current === "printers" ? (
          <Printers />
        ) : current === "connectors" ? (
          <Connectors />
        ) : current === "links" ? (
          <Links />
        ) : (
          <People users={users ?? []} me={user} onChanged={loadUsers} />
        )}
      </main>
    </div>
  );
}
