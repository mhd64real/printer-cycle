import { useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { Shell } from "@/screens/Shell";
import { api, type User } from "@/api";

const MIN_PASSWORD = 10;

/**
 * The first screen anybody sees on a new box.
 *
 * There is no setup token to type here. Core printed one, and the dashboard
 * already spent it to prove it belongs on this machine. Asking a person to
 * retype it would prove nothing further and would only be one more chance to
 * mistype something.
 */
export function Setup({ onDone }: { onDone: (user: User) => void }) {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD;

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);

    try {
      await api.setup(username, displayName || username, password);
      // Signed in straight away, with the credentials just chosen. Making
      // somebody type them again immediately would be asking them to prove
      // something they demonstrated a second ago.
      const { user } = await api.signIn(username, password);
      onDone(user);
    } catch (err) {
      setError(err instanceof Error ? err.message : "something went wrong");
      setBusy(false);
    }
  }

  return (
    <Shell
      title="Create your account"
      intro={
        <>
          This box has no accounts yet. The first one you make can administer it.
        </>
      }
    >
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          autoFocus
          required
          disabled={busy}
        />
        <Field
          label="Display name"
          hint="Optional. What other people on this box will see."
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          autoComplete="name"
          disabled={busy}
        />
        <Field
          label="Password"
          type="password"
          hint={`At least ${MIN_PASSWORD} characters. Length is what helps; nothing here asks for a symbol.`}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          required
          disabled={busy}
        />

        {tooShort ? <Notice>That password is shorter than {MIN_PASSWORD} characters.</Notice> : null}
        {error ? <Notice>{error}</Notice> : null}

        <Button type="submit" disabled={busy || tooShort || !username || !password}>
          {busy ? "Creating" : "Create account"}
        </Button>
      </form>
    </Shell>
  );
}
