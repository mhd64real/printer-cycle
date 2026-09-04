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

export type Printer = {
  id: string;
  name: string;
  queue_name: string;
  device_uri: string;
  ppd: string;
  location: string;
  restricted: boolean;
};

export type Device = {
  /**
   * Stable across a discovery run, and the right thing to key a list on.
   *
   * Discovery announces updates as well as arrivals: a printer first seen over
   * ipps is announced again under dnssd once a better description of it turns
   * up. Keying on device_uri instead means the update reads as a second
   * printer, and the same machine shows twice until the final reply lands.
   *
   * Absent on probe results, which have nothing to reconcile against.
   */
  identity?: string;
  device_uri: string;
  device_id: string;
  make_and_model: string;
  info: string;
  location: string;
  transport: string;
};

export type DriverCandidate = {
  ppd: string;
  make_and_model: string;
  recommended: boolean;
  requires_proprietary_plugin: boolean;
};

/**
 * Codes core uses for refusals a page can act on.
 *
 * Only the ones the interface does something about. The rest are reported as
 * whatever core said, which is the right thing to do with a refusal nobody has
 * written a response to yet.
 */
export const DRIVER_REQUIRED = -32008;

/** Thrown for anything the server refused, carrying what it said. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    /** Core's error code, when the refusal came from core. */
    readonly code?: number,
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
    throw new ApiError(
      message,
      response.status,
      typeof payload.code === "number" ? payload.code : undefined,
    );
  }
  return payload as T;
}

/** Asks core for something, through the dashboard, which decides what is allowed. */
async function call<T>(method: string, params?: Record<string, unknown>): Promise<T> {
  const { result } = await request<{ result: T }>("/api/call", { method, params: params ?? {} });
  return result;
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

  printers: () => call<{ printers: Printer[] }>("printers.list"),

  discover: (timeoutMs = 8000) =>
    call<{ devices: Device[] }>("printers.discover", { timeout_ms: timeoutMs }),

  probe: (address: string) => call<Device & { port: number }>("printers.probe", { address }),

  driverCandidates: (deviceId: string) =>
    call<{ candidates: DriverCandidate[] }>("printers.driverCandidates", { device_id: deviceId }),

  /** The manufacturers there are drivers for. Around eighty on a full install. */
  driverMakes: () => call<{ makes: string[] }>("printers.drivers", {}),

  /**
   * Drivers, narrowed.
   *
   * Never ask for everything: a full driver installation is close to eighteen
   * thousand, and one manufacturer alone can be three thousand of them.
   */
  drivers: (params: { make?: string; query?: string; limit?: number }) =>
    call<{ drivers: DriverCandidate[]; truncated: boolean }>("printers.drivers", params),

  addPrinter: (device: { deviceUri: string; name: string; deviceId?: string; ppd?: string }) =>
    call<Printer & { driver_chosen_automatically: boolean }>("printers.add", {
      device_uri: device.deviceUri,
      name: device.name,
      device_id: device.deviceId ?? "",
      ...(device.ppd ? { ppd: device.ppd } : {}),
    }),

  removePrinter: (id: string) => call<{ removed: string }>("printers.remove", { id }),

  /**
   * Sends a document to a printer.
   *
   * Not through /api/call. A document travels as bytes, and the relay carries
   * JSON, so this posts a form the server streams straight through to core
   * rather than holding the file anywhere along the way.
   *
   * The file goes last on purpose: the server reads the parts in order and has
   * to know which printer it is for before the document starts arriving.
   */
  print: async (printerId: string, file: File, options: PrintOptions = {}) => {
    const form = new FormData();
    form.append("printer_id", printerId);
    if (options.copies !== undefined) form.append("copies", String(options.copies));
    if (options.duplex !== undefined) form.append("duplex", String(options.duplex));
    if (options.color !== undefined) form.append("color", String(options.color));
    if (options.media) form.append("media", options.media);
    form.append("file", file, file.name);

    const response = await fetch("/api/print", {
      method: "POST",
      body: form,
      credentials: "same-origin",
    });

    const text = await response.text();
    const payload = text ? (JSON.parse(text) as Record<string, unknown>) : {};
    if (!response.ok) {
      const message =
        typeof payload.error === "string" ? payload.error : "that document could not be printed";
      throw new ApiError(
        message,
        response.status,
        typeof payload.code === "number" ? payload.code : undefined,
      );
    }
    return payload as { result: { job_id: string; state: string } };
  },
};

export type PrintOptions = {
  copies?: number;
  /** Unset means whatever the printer already does, which is not the same as off. */
  duplex?: boolean;
  color?: boolean;
  media?: string;
};

/**
 * Subscribes to what core says without being asked.
 *
 * Discovery announces printers as it finds them and jobs report their own
 * progress. Both are useless if the page can only ask, and a page that polled
 * for them would be asking a Raspberry Pi a question several times a second for
 * the sake of an answer that is almost always "nothing yet".
 */
export function subscribe(handlers: Record<string, (data: unknown) => void>): () => void {
  const source = new EventSource("/api/events");

  const registered = Object.entries(handlers).map(([event, handler]) => {
    const listener = (e: MessageEvent) => {
      try {
        handler(JSON.parse(e.data));
      } catch {
        // A frame that will not parse is not worth tearing the stream down for.
      }
    };
    source.addEventListener(event, listener as EventListener);
    return { event, listener };
  });

  return () => {
    for (const { event, listener } of registered) {
      source.removeEventListener(event, listener as EventListener);
    }
    source.close();
  };
}
