import { useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { api, type User } from "@/api";

/**
 * Adding somebody to the box.
 *
 * Its own component because it is reached from two places that never both
 * exist: from the header while this is a one-person install and there is no
 * People tab, and from the People tab once there is more than one person. One
 * form, one way in at a time.
 */
export function AddSomeone({
  onAdded,
  onCancel,
}: {
  onAdded: () => void;
  onCancel: () => void;
}) {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function add(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createUser(username.trim(), displayName.trim(), password);
      onAdded();
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not add that account");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={add} className="max-w-lg rounded-lg border border-line p-4">
      <Field
        label="Username"
        hint="What they sign in with."
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        disabled={busy}
        autoFocus
      />
      <div className="mt-3">
        <Field
          label="Name"
          hint="What they are called on screen. Optional."
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          disabled={busy}
        />
      </div>
      <div className="mt-3">
        <Field
          label="Password"
          hint="At least twelve characters. Length is what matters, so a few words beat a short one with symbols in it."
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={busy}
        />
      </div>

      <div className="mt-4 flex gap-2">
        <Button type="submit" disabled={busy || username.trim() === "" || password === ""}>
          {busy ? "Adding" : "Add them"}
        </Button>
        <Button type="button" variant="plain" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>

      {error ? (
        <div className="mt-3">
          <Notice>{error}</Notice>
        </div>
      ) : null}
    </form>
  );
}

/**
 * The people page.
 *
 * Only exists once there is more than one of them. A household of one has
 * nothing to manage: no roles to assign, no list worth reading, and a page
 * showing a single row with your own name on it is a page that made somebody
 * wonder whether they had missed a step.
 */
export function People({
  users,
  me,
  onChanged,
}: {
  users: User[];
  me: User;
  onChanged: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function remove(user: User) {
    setRemoving(user.id);
    setError(null);
    try {
      await api.removeUser(user.id);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not remove that account");
    } finally {
      setRemoving(null);
    }
  }

  return (
    <div>
      <div className="mb-6">
        {adding ? (
          <AddSomeone
            onAdded={() => {
              setAdding(false);
              onChanged();
            }}
            onCancel={() => setAdding(false)}
          />
        ) : me.is_admin ? (
          <Button onClick={() => setAdding(true)}>Add someone</Button>
        ) : null}
      </div>

      {error ? (
        <div className="mb-4 max-w-lg">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      <ul className="divide-y divide-line border-t border-line">
        {users.map((user) => (
          <li key={user.id} className="flex items-center justify-between gap-4 py-3">
            <div className="min-w-0">
              <p className="truncate font-medium">
                {user.display_name || user.username}
                {user.id === me.id ? <span className="text-muted"> (you)</span> : null}
              </p>
              <p className="truncate text-sm text-muted">
                {user.username}
                {user.is_admin ? ", administrator" : ""}
              </p>
            </div>

            {/*
              Not offered on your own row. Removing yourself is allowed by the
              protocol, and somebody leaving should not need to find another
              administrator to be let out, but a button that signs you out
              permanently sitting one row above everybody else's is a misclick
              waiting to happen, and this interface has nowhere to ask "are you
              sure" that would not be worse.
            */}
            {me.is_admin && user.id !== me.id ? (
              <Button
                variant="plain"
                onClick={() => remove(user)}
                disabled={removing === user.id}
              >
                {removing === user.id ? "Removing" : "Remove"}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}
