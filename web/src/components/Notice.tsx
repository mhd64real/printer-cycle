/** Something went wrong, said plainly and in place rather than in an alert. */
export function Notice({ children }: { children: React.ReactNode }) {
  return (
    <p
      role="alert"
      className="rounded-md border border-danger/40 bg-danger-bg px-3 py-2 text-sm text-danger"
    >
      {children}
    </p>
  );
}
