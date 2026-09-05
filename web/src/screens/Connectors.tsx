import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Field } from "@/components/Field";
import { Notice } from "@/components/Notice";
import { SettingsForm } from "@/components/SettingsForm";
import { api, type Connector } from "@/api";

/**
 * The connectors page.
 *
 * Everything but printing lives out here: AirPrint, Telegram, a phone app,
 * whatever somebody writes next. They are separate programs that talk to core
 * over a socket and cannot put code into this page, so what is rendered comes
 * entirely from what each one declared about itself when it registered.
 *
 * Nothing in this file or the form it uses names a connector.
 */
export function Connectors() {
  const [connectors, setConnectors] = useState<Connector[] | null>(null);
  const [scopes, setScopes] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [switching, setSwitching] = useState<string | null>(null);
  const [inviting, setInviting] = useState(false);

  const load = useCallback(async () => {
    try {
      const { connectors, known_scopes } = await api.connectors();
      setConnectors(connectors ?? []);
      setScopes(known_scopes ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "cannot list the connectors");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function toggle(connector: Connector) {
    setSwitching(connector.id);
    setError(null);
    try {
      await api.setConnectorEnabled(connector.id, !connector.enabled);
      setConnectors((current) =>
        (current ?? []).map((c) =>
          c.id === connector.id ? { ...c, enabled: !connector.enabled } : c,
        ),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not change that");
    } finally {
      setSwitching(null);
    }
  }

  if (connectors === null) {
    return <p className="text-sm text-muted">Loading.</p>;
  }

  return (
    <div>
      {error ? (
        <div className="mb-4 max-w-lg">
          <Notice>{error}</Notice>
        </div>
      ) : null}

      <div className="mb-6">
        {inviting ? (
          <Invite
            scopes={scopes}
            onDone={() => {
              setInviting(false);
              void load();
            }}
            onCancel={() => setInviting(false)}
          />
        ) : (
          <Button onClick={() => setInviting(true)}>Add a connector</Button>
        )}
      </div>

      {connectors.length === 0 ? (
        <p className="text-sm text-muted">
          No connectors yet. Anything that connects to this machine shows up here, with whatever
          settings it says it needs.
        </p>
      ) : (
        <ul className="space-y-6">
          {connectors.map((connector) => (
            <li key={connector.id} className="rounded-lg border border-line p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-medium">
                    {connector.name || connector.id}
                    {connector.version ? (
                      <span className="text-muted"> {connector.version}</span>
                    ) : null}
                  </p>
                  {connector.description ? (
                    <p className="mt-0.5 text-sm text-muted">{connector.description}</p>
                  ) : null}
                  <p className="mt-0.5 text-sm text-muted">{describe(connector)}</p>
                </div>

                <Button
                  variant="plain"
                  onClick={() => toggle(connector)}
                  disabled={switching === connector.id}
                >
                  {switching === connector.id
                    ? "Working"
                    : connector.enabled
                      ? "Turn off"
                      : "Turn on"}
                </Button>
              </div>

              <SettingsForm
                fields={connector.settings_schema ?? []}
                values={connector.settings ?? {}}
                disabled={!connector.enabled}
                onSave={async (key, value) => {
                  await api.setConnectorSetting(connector.id, key, value);
                }}
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Letting a program that is not here yet connect.
 *
 * It ends in a token to paste into whatever is being connected. That token is
 * shown once and cannot be recovered: only a hash of it is kept, so there is
 * nothing to look up later, and the screen says so rather than implying there
 * is somewhere to find it again.
 */
function Invite({
  scopes,
  onDone,
  onCancel,
}: {
  scopes: string[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(null);

  async function invite(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const result = await api.inviteConnector(id.trim(), name.trim(), chosen);
      setToken(result.token);
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not add that connector");
    } finally {
      setBusy(false);
    }
  }

  if (token) {
    return (
      <div className="rounded-lg border border-line p-4">
        <p className="text-sm font-medium">Give this to the connector, once.</p>
        <p className="mt-1 text-sm text-muted">
          It is not stored and cannot be shown again. If it is lost, add the connector again to
          get another.
        </p>
        <code className="mt-3 block break-all rounded-md border border-line bg-raised px-3 py-2 text-sm">
          {token}
        </code>
        <div className="mt-3">
          <Button onClick={onDone}>Done</Button>
        </div>
      </div>
    );
  }

  return (
    <form onSubmit={invite} className="max-w-lg rounded-lg border border-line p-4">
      <Field
        label="Identifier"
        hint="How the connector names itself when it connects. Lowercase, no spaces."
        placeholder="weather-sign"
        value={id}
        onChange={(e) => setId(e.target.value)}
        disabled={busy}
        autoFocus
      />
      <div className="mt-3">
        <Field
          label="Name"
          hint="What it is called on this page."
          placeholder="Weather Sign"
          value={name}
          onChange={(e) => setName(e.target.value)}
          disabled={busy}
        />
      </div>

      <fieldset className="mt-4">
        <legend className="text-sm font-medium">What it may do</legend>
        <p className="mt-0.5 text-sm text-muted">
          Give it only what it needs. Anything it was not given is refused, whatever it asks for.
        </p>
        <div className="mt-2 grid gap-1.5 sm:grid-cols-2">
          {scopes.map((scope) => (
            <label key={scope} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={chosen.includes(scope)}
                disabled={busy}
                onChange={(e) =>
                  setChosen((current) =>
                    e.target.checked
                      ? [...current, scope]
                      : current.filter((s) => s !== scope),
                  )
                }
                className="size-4 accent-accent"
              />
              <code>{scope}</code>
            </label>
          ))}
        </div>
      </fieldset>

      <div className="mt-4 flex gap-2">
        <Button type="submit" disabled={busy || id.trim() === ""}>
          {busy ? "Adding" : "Add it"}
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
 * What state a connector is in, in words.
 *
 * Enabled and connected are genuinely different and both matter: one is whether
 * it is allowed to work, the other whether it is currently running. A connector
 * that is on but not connected is the normal way to notice that something has
 * crashed or has not been started.
 */
function describe(connector: Connector): string {
  if (!connector.enabled) return "Turned off";
  if (!connector.enrolled) return "On, waiting to be enrolled";
  return connector.connected ? "On, connected" : "On, not running";
}
