import type {
  Catalog,
  Command,
  ConnectionsEnvelope,
  DeviceList,
  Envelope,
  Session,
  Settings,
  SharedConnections,
  UpdateInfo,
  VoicePairingState,
} from "./types";
import { apiErrorMessage } from "./error-messages";

export class ApiError extends Error {
  constructor(
    readonly code: string,
    readonly status: number,
    readonly body?: unknown,
  ) {
    super(apiErrorMessage(code, status));
  }
}

async function decode<T>(response: Response): Promise<T> {
  if (!response.ok) {
    let body: unknown;
    const contentType = response.headers.get("Content-Type") ?? "";
    const raw = await response.text().catch(() => "");
    try {
      body = JSON.parse(raw);
    } catch {
      body = undefined;
    }
    const record =
      body && typeof body === "object" ? (body as Record<string, unknown>) : {};
    const safeText =
      contentType.toLowerCase().includes("text/plain") &&
      /^[A-Za-z0-9 _.-]{1,100}$/.test(raw.trim())
        ? raw.trim()
        : "";
    throw new ApiError(
      typeof record.error === "string"
        ? record.error
        : safeText || `Request failed (${response.status})`,
      response.status,
      body,
    );
  }
  return response.status === 204
    ? (undefined as T)
    : (response.json().catch(() => { throw new ApiError("invalid_response", 502); }) as Promise<T>);
}

export class AdminApi {
  private csrf?: string;
  setCsrf(token?: string) {
    this.csrf = token;
  }
  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const method = (init.method ?? "GET").toUpperCase();
    const headers = new Headers(init.headers);
    if (init.body && !headers.has("Content-Type"))
      headers.set("Content-Type", "application/json");
    if (!["GET", "HEAD", "OPTIONS"].includes(method) && this.csrf)
      headers.set("X-CSRF-Token", this.csrf);
    const response = await fetch(path, {
      ...init,
      method,
      headers,
      credentials: "same-origin",
      cache: "no-store",
    }).catch((error: unknown) => {
      if (error instanceof Error && error.name === "AbortError") throw error;
      throw new ApiError("network_error", 0);
    });
    if (response.status === 401 && path !== "/api/admin/v1/session")
      window.dispatchEvent(new CustomEvent("admin-auth-expired"));
    return decode<T>(response);
  }
  session() {
    return this.request<Session>("/api/admin/v1/session");
  }
  async login(password: string) {
    const value = await this.request<Session>("/api/admin/v1/login", {
      method: "POST",
      body: JSON.stringify({ password }),
    }).catch((error: unknown) => {
      if (error instanceof ApiError && error.status === 401) throw new ApiError("invalid_password", 401, error.body);
      throw error;
    });
    this.setCsrf(value.csrf_token);
    return value;
  }
  async logout() {
    return this.request<void>("/api/admin/v1/logout", {
      method: "POST",
      body: "{}",
    });
  }
  calendarStatus(deviceId: string, signal?: AbortSignal) {
    return this.request<import("./calendar-status").CalendarDetails>(this.devicePath(deviceId, "calendar-status"), {signal});
  }
  devices() {
    return this.request<DeviceList>("/api/admin/v1/devices");
  }
  async settings(deviceId: string) {
    const result = normalizeEnvelope(
      await this.request<Envelope>(this.devicePath(deviceId, "settings")),
    );
    if (result.device_id !== deviceId) {
      throw new ApiError("Device response did not match the selected display", 502);
    }
    return result;
  }
  async save(
    deviceId: string,
    settings: Settings,
    revision: number,
    network_credentials?: Record<string, string>,
  ) {
    const body: Record<string, unknown> = { settings };
    if (network_credentials && Object.keys(network_credentials).length)
      body.network_credentials = network_credentials;
    const result = await this.request<Envelope>(
      this.devicePath(deviceId, "settings"),
      {
      method: "PATCH",
      headers: { "If-Match": `"${revision}"` },
      body: JSON.stringify(body),
      },
    );
    if (result.device_id !== deviceId) {
      throw new ApiError("Device response did not match the selected display", 502);
    }
    return normalizeEnvelope(result);
  }
  connections() {
    return this.request<ConnectionsEnvelope>("/api/admin/v1/connections");
  }
  saveConnections(
    connections: SharedConnections,
    revision: number,
    secrets?: Record<string, string | null>,
    confirmDisconnect = false,
  ) {
    const body: Record<string, unknown> = { connections };
    if (secrets && Object.keys(secrets).length) body.secrets = secrets;
    if (confirmDisconnect) body.confirm_disconnect = true;
    return this.request<ConnectionsEnvelope>("/api/admin/v1/connections", {
      method: "PATCH",
      headers: { "If-Match": `"${revision}"` },
      body: JSON.stringify(body),
    });
  }
  catalog(deviceId: string, kind: "albums" | "calendars") {
    return this.request<Catalog>(this.devicePath(deviceId, kind));
  }
  entities(deviceId: string) {
    return this.request<Catalog>(this.devicePath(deviceId, "entities"));
  }
  updates(deviceId: string) {
    return this.request<UpdateInfo>(this.devicePath(deviceId, "updates"));
  }
  async voicePairing(deviceId: string, signal?: AbortSignal): Promise<VoicePairingState> {
    const result = await this.request<unknown>(this.devicePath(deviceId, "voice"), { signal });
    return normalizeVoicePairingState(result, deviceId);
  }
  async pairVoice(deviceId: string, pairingCode: string, wssOrigin: string): Promise<VoicePairingState> {
    try {
      const result = await this.request<unknown>(this.devicePath(deviceId, "voice/pair"), {
        method: "POST",
        body: JSON.stringify({ pairing_code: pairingCode, wss_origin: wssOrigin }),
      });
      return normalizeVoicePairingState(result, deviceId);
    } catch (error) {
      throw sanitizedVoiceError(error);
    }
  }
  async disableVoice(deviceId: string): Promise<VoicePairingState> {
    return this.mutateVoice(deviceId, "voice/disable");
  }
  async cancelVoicePairing(deviceId: string): Promise<VoicePairingState> {
    return this.mutateVoice(deviceId, "voice/cancel");
  }
  private async mutateVoice(deviceId: string, path: string): Promise<VoicePairingState> {
    try {
      const result = await this.request<unknown>(this.devicePath(deviceId, path), { method: "POST", body: "{}" });
      return normalizeVoicePairingState(result, deviceId);
    } catch (error) {
      throw sanitizedVoiceError(error);
    }
  }
  action(
    deviceId: string,
    action: "photo_start" | "restart" | "ota_check" | "ota_install" | "reset" | "backlight_preview" | "backlight_adjust" | "backlight_begin" | "backlight_restore",
    percent?: number,
  ) {
    const confirm = ["restart", "ota_install", "reset"].includes(action)
      ? true
      : undefined;
    return this.request<{ command: Command }>(this.devicePath(deviceId, "actions"), {
      method: "POST",
      body: JSON.stringify({ action, ...(confirm ? { confirm } : {}), ...(percent !== undefined ? { percent } : {}) }),
    });
  }
  async diagnostics(deviceId: string) {
    const path = this.devicePath(deviceId, "diagnostics");
    const response = await fetch(path, {
      credentials: "same-origin",
      cache: "no-store",
    });
    if (response.status === 401)
      window.dispatchEvent(new CustomEvent("admin-auth-expired"));
    if (!response.ok) return decode<never>(response);
    const requestedFilename = response.headers
      .get("Content-Disposition")
      ?.match(/filename="?([^";]+)"?/)?.[1];
    const filename = requestedFilename
      ?.replaceAll(/[^a-zA-Z0-9._-]/g, "_")
      .slice(0, 100);
    return {
      blob: await response.blob(),
      filename: filename || "display-diagnostics.json",
    };
  }
  private devicePath(deviceId: string, suffix: string) {
    if (!/^[a-f0-9]{32}$/.test(deviceId)) {
      throw new ApiError("Invalid display identifier", 400);
    }
    return `/api/admin/v1/devices/${deviceId}/${suffix}`;
  }
}

const voicePairingStatuses = new Set<VoicePairingState["status"]>([
  "unconfigured", "pending", "applied", "connected", "revoked_cleanup_pending", "error",
]);

function normalizeVoicePairingState(value: unknown, expectedDeviceId: string): VoicePairingState {
  if (!value || typeof value !== "object") throw new ApiError("invalid_response", 502);
  const state = value as Record<string, unknown>;
  if (state.device_id !== expectedDeviceId || typeof state.wss_origin !== "string" ||
      !voicePairingStatuses.has(state.status as VoicePairingState["status"]) ||
      (state.wss_origin !== "" && normalizeWssOrigin(state.wss_origin) !== state.wss_origin)) {
    throw new ApiError("invalid_response", 502);
  }
  // Return only the public state fields; never retain unexpected response fields.
  return { device_id: expectedDeviceId, wss_origin: state.wss_origin, status: state.status as VoicePairingState["status"] };
}

/** Converts a user-entered WSS origin to the backend's strict canonical authority form. */
export function normalizeWssOrigin(value: string): string | null {
  const input = value.trim();
  if (!input || input.length > 159) return null;
  const match = input.match(/^wss:\/\/([^/?#]*)(\/?$)/i);
  if (!match || !match[1] || match[1].includes("@")) return null;
  let parsed: URL;
  try { parsed = new URL(input); } catch { return null; }
  if (parsed.protocol !== "wss:" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash || /[?#]/.test(input)) return null;

  const authority = match[1];
  let rawHost: string;
  let rawPort: string | null = null;
  if (authority.startsWith("[")) {
    const close = authority.indexOf("]");
    if (close < 0) return null;
    rawHost = authority.slice(0, close + 1);
    const tail = authority.slice(close + 1);
    if (tail) {
      if (!tail.startsWith(":") || tail.length === 1) return null;
      rawPort = tail.slice(1);
    }
    if (!rawHost.endsWith("]") || !parsed.hostname.startsWith("[") || parsed.hostname.includes("%") || parsed.hostname.toLowerCase().startsWith("[::ffff:")) return null;
  } else {
    const firstColon = authority.indexOf(":");
    const lastColon = authority.lastIndexOf(":");
    if (firstColon !== lastColon) return null;
    rawHost = firstColon < 0 ? authority : authority.slice(0, firstColon);
    if (firstColon >= 0) {
      rawPort = authority.slice(firstColon + 1);
      if (!rawPort) return null;
    }
    if (!parsed.hostname || parsed.hostname.startsWith("[") || parsed.hostname.endsWith(".")) return null;
    const labels = parsed.hostname.split(".");
    if (parsed.hostname.length > 253 || labels.some((label) => !label || label.length > 63 || label.startsWith("-") || label.endsWith("-") || !/^[a-z0-9-]+$/.test(label))) return null;
  }

  if (rawPort !== null && !/^(0|[1-9][0-9]*)$/.test(rawPort)) return null;
  const port = rawPort === null ? 443 : Number(rawPort);
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
  const host = parsed.hostname.toLowerCase();
  if (!host || rawHost === "") return null;
  return `wss://${host}:${port}`;
}

function sanitizedVoiceError(error: unknown): Error {
  if (error instanceof ApiError) return new ApiError(error.code, error.status);
  return new ApiError("network_error", 0);
}

export function isRevisionConflict(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 409 &&
    !!error.body &&
    typeof error.body === "object" &&
    (error.body as Record<string, unknown>).error === "revision_conflict"
  );
}

function normalizeEnvelope(envelope: Envelope): Envelope {
  const services = envelope.settings.services;
  const screen = envelope.settings.screen ?? ({} as Envelope["settings"]["screen"]);
  const locale = screen.locale;
  return {
    ...envelope,
    settings: {
      ...envelope.settings,
      screen: {
        ...screen,
        locale: locale === "en" || locale === "sr-Latn" ? locale : "ru",
      },
      services: {
        ...services,
        calendar: {
          ...services.calendar,
          ids: Array.isArray(services.calendar?.ids)
            ? services.calendar.ids
            : [],
        },
        transport: {
          ...services.transport,
          ids: Array.isArray(services.transport?.ids)
            ? services.transport.ids
            : [],
        },
        home: {
          ...services.home,
          sensors: Array.isArray(services.home?.sensors)
            ? services.home.sensors
            : [],
          lights: Array.isArray(services.home?.lights)
            ? services.home.lights
            : [],
          favorites: Array.isArray(services.home?.favorites) ? services.home.favorites : [],
          climates: Array.isArray(services.home?.climates)
            ? services.home.climates
            : [],
        },
      },
    },
    capabilities: {
      ...envelope.capabilities,
      locales: Array.isArray(envelope.capabilities?.locales)
        ? envelope.capabilities.locales.filter((value) => value === "ru" || value === "en" || value === "sr-Latn")
        : ["ru", "en", "sr-Latn"],
    },
    device: {
      ...envelope.device,
      capabilities: Array.isArray(envelope.device?.capabilities) ? envelope.device.capabilities : [],
    },
  };
}
