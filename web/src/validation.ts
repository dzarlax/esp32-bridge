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
