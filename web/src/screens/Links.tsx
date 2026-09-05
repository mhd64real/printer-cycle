import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { api, type Connector, type IdentityLink } from "@/api";

/**
 * The screen that answers "what can reach my printing, and how do I stop it".
 *
 * It exists in one place because core owns the bindings. A connector decides how
 * a pairing code is delivered, and nothing else: it cannot say who anybody is,
 * and it cannot see or undo a link it did not make. So there is one list here
 * rather than a separate account page inside every connector somebody installs.
 */
export function Links() {
  const [links, setLinks] = useState<IdentityLink[] | null>(null);
  const [names, setNames] = useState<Record<string, string>>({});
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [approved, setApproved] = useState(false);
  const [revoking, setRevoking] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [{ links }, { connectors }] = await Promise.all([api.links(), api.connectors()]);
      setLinks(links ?? []);
      setNames(
        Object.fromEntries((connectors ?? []).map((c: Connector) => [c.id, c.name || c.id])),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "cannot list what is linked");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function approve(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setApproved(false);
    try {
      await api.approveLink(code.trim());
      setApproved(true);
      setCode("");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "that code was not accepted");
    } finally {
      setBusy(false);
    }
  }

  async function revoke(link: IdentityLink) {
    setRevoking(link.id);
    setError(null);
    try {
      await api.revokeLink(link.id);
      setLinks((current) => (current ?? []).filter((l) => l.id !== link.id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not unlink that");
    } finally {
      setRevoking(null);
    }
  }

  return (
    <div>
      <form onSubmit={approve} className="max-w-lg">
        <Field
          label="Pairing code"
          hint="Whatever you are connecting shows you a code. Type it here to link it to this account."
          placeholder="2JG6-7F6W"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          disabled={busy}
        />
        <div className="mt-3 flex items-center gap-3">
          <Button type="submit" disabled={busy || code.trim() === ""}>
            {busy ? "Linking" : "Link it"}
          </Button>
          {approved ? <span className="text-sm text-muted">Linked.</span> : null}
        </div>
      </form>

      {error ? (
        <div className="mt-4 max-w-lg">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      <div className="mt-8">
        {links === null ? (
          <p className="text-sm text-muted">Loading.</p>
        ) : links.length === 0 ? (
          <p className="text-sm text-muted">
            Nothing is linked to this account yet.
          </p>
        ) : (
          <ul className="divide-y divide-line border-t border-line">
            {links.map((link) => (
              <li key={link.id} className="flex items-center justify-between gap-4 py-3">
                <div className="min-w-0">
                  <p className="truncate font-medium">{link.display || link.external_id}</p>
                  <p className="truncate text-sm text-muted">
                    {names[link.connector_id] ?? link.connector_id}
                    {link.display ? `, ${link.external_id}` : ""}
                  </p>
                </div>
                <Button
                  variant="plain"
                  onClick={() => revoke(link)}
                  disabled={revoking === link.id}
                >
                  {revoking === link.id ? "Unlinking" : "Unlink"}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
