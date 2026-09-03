import { useEffect, useState } from "react";

import { Setup } from "@/screens/Setup";
import { SignIn } from "@/screens/SignIn";
import { SignedIn } from "@/screens/SignedIn";
import { api, type User } from "@/api";

type State =
  | { kind: "loading" }
  | { kind: "setup" }
  | { kind: "signin" }
  | { kind: "in"; user: User }
  | { kind: "unreachable" };

export default function App() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    void decideWhereToStart().then(setState);
  }, []);

  switch (state.kind) {
    case "loading":
      return <Centred>Checking this box.</Centred>;

    case "unreachable":
      return (
        <Centred>
          The dashboard cannot reach printer-cycle. It may still be starting.
        </Centred>
      );

    case "setup":
      return <Setup onDone={(user) => setState({ kind: "in", user })} />;

    case "signin":
      return <SignIn onDone={(user) => setState({ kind: "in", user })} />;

    case "in":
      return <SignedIn user={state.user} onSignOut={() => setState({ kind: "signin" })} />;
  }
}

/**
 * Works out which of three situations somebody is in.
 *
 * Asked in this order deliberately. A box with no accounts needs setting up
 * whether or not a stale cookie is lying around, and a stale cookie is exactly
 * what somebody has after reinstalling.
 */
async function decideWhereToStart(): Promise<State> {
  try {
    const { needs_setup } = await api.needsSetup();
    if (needs_setup) {
      return { kind: "setup" };
    }
  } catch {
    return { kind: "unreachable" };
  }

  try {
    const { user } = await api.me();
    return { kind: "in", user };
  } catch {
    // Not signed in, which is the ordinary case rather than a failure.
    return { kind: "signin" };
  }
}

function Centred({ children }: { children: React.ReactNode }) {
  return (
    <main className="mx-auto flex min-h-dvh max-w-md items-center justify-center px-6 text-muted">
      <p>{children}</p>
    </main>
  );
}
