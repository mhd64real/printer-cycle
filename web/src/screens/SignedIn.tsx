import { Button } from "@/components/Button";
import { Printers } from "@/screens/Printers";
import { api, type User } from "@/api";

export function SignedIn({ user, onSignOut }: { user: User; onSignOut: () => void }) {
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

      <main className="mt-8">
        <Printers />
      </main>
    </div>
  );
}
