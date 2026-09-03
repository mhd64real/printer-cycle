import { useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { Shell } from "@/screens/Shell";
import { api, type User } from "@/api";

export function SignIn({ onDone }: { onDone: (user: User) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);

    try {
      const { user } = await api.signIn(username, password);
      onDone(user);
    } catch (err) {
      // Whatever went wrong, the same sentence. The server already refuses to
      // say whether a name exists, and contradicting it here would give away
      // exactly what it withheld.
      setError(err instanceof Error ? err.message : "incorrect username or password");
      setBusy(false);
    }
  }

  return (
    <Shell title="Sign in">
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
          label="Password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          required
          disabled={busy}
        />

        {error ? <Notice>{error}</Notice> : null}

        <Button type="submit" disabled={busy || !username || !password}>
          {busy ? "Signing in" : "Sign in"}
        </Button>
      </form>
    </Shell>
  );
}
