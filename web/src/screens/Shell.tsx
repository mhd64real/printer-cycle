import type { ReactNode } from "react";

/** The frame every unauthenticated screen sits in. */
export function Shell({
  title,
  intro,
  children,
}: {
  title: string;
  intro?: ReactNode;
  children: ReactNode;
}) {
  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-6 py-12">
      <h1 className="text-2xl font-semibold tracking-tight">printer-cycle</h1>
      <h2 className="mt-6 text-lg font-medium">{title}</h2>
      {intro ? <div className="mt-2 text-sm text-muted">{intro}</div> : null}
      <div className="mt-6">{children}</div>
    </main>
  );
}
