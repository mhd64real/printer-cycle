import { useState, type FormEvent } from "react";

import { Button } from "@/components/Button";
import { Notice } from "@/components/Notice";
import type { SecretValue, SettingField } from "@/api";

/**
 * A settings form built from a schema, for a connector this page has never
 * heard of.
 *
 * The point of the whole arrangement: a connector is a separate program, written
 * by somebody else, that cannot put code into this page. It declares what it
 * needs as data when it registers, and this renders that. Nothing here names a
 * connector, and adding one to the system requires no change to this file.
 *
 * Saved one field at a time rather than as a form. A connector is told about a
 * change the moment it happens, so a half-filled form that saved everything at
 * once would hand it a token before the setting that says what to do with it,
 * and an unsaved field would look identical to a saved one.
 */
export function SettingsForm({
  fields,
  values,
  onSave,
  disabled,
}: {
  fields: SettingField[];
  values: Record<string, unknown>;
  onSave: (key: string, value: unknown) => Promise<void>;
  disabled?: boolean;
}) {
  if (fields.length === 0) {
    return <p className="text-sm text-muted">Nothing to configure.</p>;
  }

  return (
    <div className="mt-3 space-y-4">
      {fields.map((field) => (
        <SettingRow
          key={field.key}
          field={field}
          value={values[field.key]}
          onSave={onSave}
          disabled={disabled}
        />
      ))}
    </div>
  );
}

function isSecretValue(value: unknown): value is SecretValue {
  return typeof value === "object" && value !== null && "secret" in value;
}

/** The starting text for a field, given whatever the server sent for it. */
function initialText(field: SettingField, value: unknown): string {
  if (isSecretValue(value)) return "";
  if (value === undefined || value === null) {
    return field.default === undefined || field.default === null ? "" : String(field.default);
  }
  return String(value);
}

function SettingRow({
  field,
  value,
  onSave,
  disabled,
}: {
  field: SettingField;
  value: unknown;
  onSave: (key: string, value: unknown) => Promise<void>;
  disabled?: boolean;
}) {
  const [text, setText] = useState(() => initialText(field, value));
  const [checked, setChecked] = useState(
    () => value === true || (value === undefined && field.default === true),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const secretIsSet = isSecretValue(value) && value.set;
  const id = `setting-${field.key}`;

  async function save(next: unknown) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await onSave(field.key, next);
      setSaved(true);
      // A secret is never read back, so the box is cleared rather than left
      // holding something that looks like the stored value and is not.
      if (field.type === "secret") setText("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not save that");
    } finally {
      setBusy(false);
    }
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    if (field.type === "int") {
      const parsed = Number(text);
      if (!Number.isInteger(parsed)) {
        setError("that has to be a whole number");
        return;
      }
      void save(parsed);
      return;
    }
    void save(text);
  }

  // A switch saves as it is flipped: there is no half-flipped state to confirm.
  if (field.type === "bool") {
    return (
      <div>
        <label htmlFor={id} className="flex items-start gap-3">
          <input
            id={id}
            type="checkbox"
            checked={checked}
            disabled={disabled || busy}
            onChange={(e) => {
              setChecked(e.target.checked);
              void save(e.target.checked);
            }}
            className="mt-1 size-4 accent-accent"
          />
          <span>
            <span className="block text-sm font-medium">{field.label}</span>
            {field.description ? (
              <span className="block text-sm text-muted">{field.description}</span>
            ) : null}
          </span>
        </label>
        {error ? (
          <div className="mt-2">
            <Notice>{error}</Notice>
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <form onSubmit={submit}>
      <label htmlFor={id} className="block text-sm font-medium">
        {field.label}
        {field.required ? <span className="text-muted"> (required)</span> : null}
      </label>
      {field.description ? (
        <p className="mt-0.5 text-sm text-muted">{field.description}</p>
      ) : null}

      <div className="mt-1.5 flex flex-wrap items-start gap-2">
        {field.type === "enum" ? (
          <select
            id={id}
            value={text}
            onChange={(e) => setText(e.target.value)}
            disabled={disabled || busy}
            className="rounded-md border border-line bg-raised px-3 py-2 text-ink
                       disabled:opacity-60"
          >
            {(field.options ?? []).map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        ) : field.type === "text" ? (
          <textarea
            id={id}
            value={text}
            onChange={(e) => setText(e.target.value)}
            disabled={disabled || busy}
            rows={3}
            className="min-w-64 flex-1 rounded-md border border-line bg-raised px-3 py-2
                       text-ink placeholder:text-muted disabled:opacity-60"
          />
        ) : (
          <input
            id={id}
            type={field.type === "secret" ? "password" : field.type === "int" ? "number" : "text"}
            value={text}
            min={field.min}
            max={field.max}
            onChange={(e) => setText(e.target.value)}
            disabled={disabled || busy}
            placeholder={secretIsSet ? "Set. Type a new one to replace it." : undefined}
            autoComplete={field.type === "secret" ? "new-password" : undefined}
            className="min-w-56 flex-1 rounded-md border border-line bg-raised px-3 py-2
                       text-ink placeholder:text-muted disabled:opacity-60"
          />
        )}

        <Button type="submit" variant="plain" disabled={disabled || busy}>
          {busy ? "Saving" : "Save"}
        </Button>
      </div>

      {saved ? <p className="mt-1 text-sm text-muted">Saved.</p> : null}
      {error ? (
        <div className="mt-2">
          <Notice>{error}</Notice>
        </div>
      ) : null}
    </form>
  );
}
