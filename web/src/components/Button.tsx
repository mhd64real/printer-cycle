import type { ButtonHTMLAttributes } from "react";

import { cn } from "@/lib/cn";

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "plain";
};

/**
 * A button.
 *
 * Hover changes colour and nothing else: no lift, no scale, no shadow. A control
 * that moves under the pointer is a control that can be missed.
 */
export function Button({ variant = "primary", className, ...props }: Props) {
  return (
    <button
      className={cn(
        "rounded-md px-4 py-2 text-sm font-medium transition-colors",
        "disabled:cursor-not-allowed disabled:opacity-60",
        variant === "primary" && "bg-accent text-accent-ink hover:bg-accent/90",
        variant === "plain" && "border border-line text-ink hover:bg-line/40",
        className,
      )}
      {...props}
    />
  );
}
