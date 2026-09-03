import type { InputHTMLAttributes } from "react";

type Props = InputHTMLAttributes<HTMLInputElement> & {
  label: string;
  hint?: string;
};

/** A labelled input. The label is a real label, so tapping it focuses the field. */
export function Field({ label, hint, id, ...props }: Props) {
  const inputId = id ?? `field-${label.toLowerCase().replace(/\s+/g, "-")}`;

  return (
    <div className="space-y-1.5">
      <label htmlFor={inputId} className="block text-sm font-medium">
        {label}
      </label>
      <input
        id={inputId}
        className="w-full rounded-md border border-line bg-raised px-3 py-2
                   text-ink placeholder:text-muted
                   disabled:opacity-60"
        {...props}
      />
      {hint ? <p className="text-sm text-muted">{hint}</p> : null}
    </div>
  );
}
