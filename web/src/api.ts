/**
 * Everything the page knows how to ask for.
 *
 * The page never talks to core. It asks the dashboard process, which holds the
 * connector key and decides what a page is allowed to request. So this file is
 * deliberately small: adding a capability means adding it on the server too,
 * which is where the decision belongs.
 */

export type User = {
  id: string;
  username: string;
  display_name: string;
  is_admin: boolean;
};

/** Thrown for anything the server refused, carrying what it said. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function request<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    // The session lives in a cookie the page cannot read, so it has to be sent
    // rather than attached by hand.
    credentials: "same-origin",
  });

  const text = await response.text();
  const payload = text ? (JSON.parse(text) as Record<string, unknown>) : {};

  if (!response.ok) {
    const message = typeof payload.error === "string" ? payload.error : "something went wrong";
    throw new ApiError(message, response.status);
  }
  return payload as T;
}

export const api = {
  needsSetup: () => request<{ needs_setup: boolean }>("/api/needs-setup"),

  me: () => request<{ user: User }>("/api/me"),

  setup: (username: string, displayName: string, password: string) =>
    request<{ user: User }>("/api/setup", {
      username,
      display_name: displayName,
      password,
    }),

  signIn: (username: string, password: string) =>
    request<{ user: User }>("/api/login", { username, password }),

  signOut: () => request<{ signed_out: boolean }>("/api/logout", {}),
};
