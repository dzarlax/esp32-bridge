import type { Envelope, Settings } from "./types";

export type ApplicationState = "applied" | "waiting" | "waiting-offline" | "not-confirmed";

export function hasCurrentApplicationAck(envelope: Envelope): boolean {
  const { device, revision } = envelope;
  if (revision <= 0 || device.applied_revision !== revision || !device.last_seen) return false;
  const lastSeen = Date.parse(device.last_seen);
  if (!Number.isFinite(lastSeen)) return false;

  // Bridge suppresses applied_revision when firmware cannot confirm the requested locale.
  // Keep the same guard here so stale or older envelopes cannot claim a false acknowledgement.
  const desiredLocale = envelope.settings.screen.locale ?? "ru";
  const localeMatches = device.capabilities.includes("locale")
    ? device.applied_locale === desiredLocale
    : desiredLocale === "ru";
  const screen = envelope.settings.screen;
  const unsupportedFavorites = (envelope.settings.services?.home?.favorites?.length ?? 0) > 0 && !device.capabilities.includes("home_favorites");
  const brightnessRequested = screen.backlight_percent != null || screen.night_backlight_percent != null || (screen.night_action != null && screen.night_action !== "off");
  return localeMatches && !unsupportedFavorites && !(brightnessRequested && !device.capabilities.includes("backlight_brightness"));
}

export function applicationState(
  envelope: Envelope,
  pendingSince: number,
  now = Date.now(),
): ApplicationState {
  const current = currentApplicationState(envelope);
  if (current === "applied") return current;
  if (current === "waiting-offline") return current;
  if (now - pendingSince >= 45_000) return "not-confirmed";
  return current;
}

export function currentApplicationState(envelope: Envelope): Exclude<ApplicationState, "not-confirmed"> {
  if (hasCurrentApplicationAck(envelope)) return "applied";
  return envelope.device.connected ? "waiting" : "waiting-offline";
}

export function sameSettings(left: Settings, right: Settings): boolean {
  return stableValue(left) === stableValue(right);
}

function stableValue(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableValue).join(",")}]`;
  if (value && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return `{${Object.keys(record).sort().map((key) => `${JSON.stringify(key)}:${stableValue(record[key])}`).join(",")}}`;
  }
  return JSON.stringify(value) ?? "undefined";
}
