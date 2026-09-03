import { Button } from "@/components/Button";
import { api, type User } from "@/api";

/**
 * What a signed-in person sees, for now.
 *
 * The screens that matter arrive from stage 46. This exists so signing in leads
 * somewhere rather than nowhere, and so the session can be seen to survive a
 * reload.
 */
export function SignedIn({ user, onSignOut }: { user: User; onSignOut: () => void }) {
  async function signOut() {
    await api.signOut().catch(() => undefined);
    onSignOut();
  }

  return (
    <main className="mx-auto max-w-2xl px-6 py-12">
      <header className="flex items-baseline justify-between gap-4">
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

      <p className="mt-8 text-muted">
        You are signed in. Printers, printing and connectors arrive in the next
        stages; see PLAN.md, phase 5.
      </p>
    </main>
  );
}
