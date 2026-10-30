import type { Settings } from "./types";

export const photoTimingLimits = {
  idle_seconds: { min: 30, max: 3600, label: "Запуск после бездействия" },
  interval_seconds: { min: 15, max: 3600, label: "Интервал смены фотографии" },
} as const;
export type PhotoTiming = keyof typeof photoTimingLimits;

export function numberError(value: number, min: number, max: number, unit: string) {
  return !Number.isInteger(value) || value < min || value > max
    ? `Введите целое число от ${min} до ${max} ${unit}.`
    : null;
}

export function photoTimingError(photos: Settings["photos"]) {
  for (const field of Object.keys(photoTimingLimits) as PhotoTiming[]) {
    const limit = photoTimingLimits[field];
    const error = numberError(photos[field], limit.min, limit.max, "сек");
    if (error) return { field, message: `${limit.label}: ${error}` };
  }
  return null;
}

export function homeEntityLengthError(home: Settings["services"]["home"], previous: Settings["services"]["home"]) {
  for (const [field, limit] of [["lights", 47], ["climates", 47], ["sensors", 128]] as const) {
    const previousIds = previous[field];
    if (home[field].some((id) => !previousIds.includes(id) && new TextEncoder().encode(id).length > limit)) {
      return "Home Assistant entity ID exceeds device limit";
    }
  }
  return null;
}

export function hasUnresolvedHomeFavorite(home: Settings["services"]["home"], previous: Settings["services"]["home"]) {
  return (home.favorites ?? []).some((favorite) => {
    const selected = favorite.kind === "light" ? home.lights : home.climates;
    if (selected.includes(favorite.entity_id)) return false;
    const wasConfigured = previous.lights.includes(favorite.entity_id) || previous.climates.includes(favorite.entity_id);
    const unchanged = (previous.favorites ?? []).some((old) => old.kind === favorite.kind &&
      old.entity_id === favorite.entity_id && (old.label ?? "") === (favorite.label ?? ""));
    return wasConfigured || !unchanged;
  });
}
