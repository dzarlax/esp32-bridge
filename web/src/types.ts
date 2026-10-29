export type ScreenSettings = {
  name: string;
  city: string;
  timezone: string;
  clock_format: "24h" | "12h";
  idle_action: "photos" | "off";
  night_enabled: boolean;
  night_start: string;
  night_end: string;
  auto_update_check: boolean;
  auto_install: boolean;
};
export type PhotoSettings = {
  enabled: boolean;
  source: "none" | "immich" | "fixture";
  base_url: string;
  album_id: string;
  idle_seconds: number;
  interval_seconds: number;
  order: "shuffle" | "sequential";
  layout: "adaptive" | "contain" | "cover";
  crop_limit_percent: number;
};
export type ServiceSettings = {
  weather: {
    enabled: boolean;
    latitude: number;
    longitude: number;
    timezone: string;
  };
  calendar: {
    enabled: boolean;
    base_url: string;
    ids: string[];
    timezone: string;
  };
  transport: { enabled: boolean; base_url: string; ids: string[] };
  health: { enabled: boolean; base_url: string };
  tasks: { enabled: boolean };
  news: {
    enabled: boolean;
    base_url: string;
    limit: number;
    since_hours: number;
  };
  home: {
    enabled: boolean;
    base_url: string;
    sensors: string[];
    lights: string[];
    climates: string[];
  };
};
export type Settings = {
  screen: ScreenSettings;
  photos: PhotoSettings;
  services: ServiceSettings;
  network: { bridge_url: string };
};
export type DeviceProfile = {
  id: string;
  name: string;
  connected: boolean;
  last_seen: string | null;
  applied_revision: number;
  desired_revision: number;
  firmware: string;
  ip: string;
  ssid: string;
};
export type DeviceList = {
  devices: DeviceProfile[];
  max_devices: number;
};
export type SharedConnections = {
  immich: { base_url: string };
  health: { base_url: string };
  tasks: Record<string, never>;
  news: { base_url: string };
  home: { base_url: string };
  calendar: { base_url: string };
  transport: { base_url: string };
};
export type ConnectionsEnvelope = {
  revision: number;
  connections: SharedConnections;
  secrets_present: Record<SecretKey, boolean>;
};
export type Device = {
  connected: boolean;
  last_seen: string | null;
  applied_revision: number;
  firmware: string;
  ip: string;
  ssid: string;
  uptime_seconds: number;
  time_synced: boolean;
  capabilities: string[];
  error: string;
  command: Command | null;
  model?: string;
  width?: number;
  height?: number;
  ota_progress?: number;
};
export type Command = {
  id: string;
  action: string;
  state: string;
  created_at: string;
  expires_at: string;
  error?: string;
};
export type Status = {
  state: "disabled" | "unconfigured" | "ready" | "error" | "unknown";
  last_success?: string;
  error?: string;
};
export type Envelope = {
  device_id: string;
  revision: number;
  settings: Settings;
  secrets_present: Record<string, boolean>;
  device: Device;
  statuses: Record<string, Status>;
  capabilities: {
    brightness: boolean;
    google_photos: boolean;
    timezones: string[];
  };
};
export type Session = {
  authenticated: boolean;
  password_enabled: boolean;
  cloudflare_enabled: boolean;
  csrf_token?: string;
  identity?: { method: string; email?: string };
};
export type UpdateInfo = {
  current_version: string;
  available_version: string;
  update_available: boolean;
  device_connected: boolean;
  history?: { version: string; state: string; at?: string }[];
};

export const SECTION_LIST = [
  {
    id: "screen",
    title: "Экран",
    subtitle: "Изображение, время и режим покоя",
    icon: "monitor",
  },
  {
    id: "photos",
    title: "Фоторамка",
    subtitle: "Фотографии на домашнем экране",
    icon: "image",
  },
  {
    id: "services",
    title: "Данные и сервисы",
    subtitle: "Источники данных и отображение",
    icon: "database",
  },
  {
    id: "network",
    title: "Сеть и Bridge",
    subtitle: "Подключение дисплея и доступ",
    icon: "wifi",
  },
  {
    id: "updates",
    title: "Обновления",
    subtitle: "Версии и действия с прошивкой",
    icon: "refresh",
  },
  {
    id: "device",
    title: "Об устройстве",
    subtitle: "Состояние и диагностика",
    icon: "info",
  },
] as const;
export type SectionId = (typeof SECTION_LIST)[number]["id"];

export const SECRET_KEYS = [
  "health_api_key",
  "todoist_token",
  "ha_token",
  "calendar_platform_token",
  "immich_api_key",
] as const;
export type SecretKey = (typeof SECRET_KEYS)[number];

export function clone<T>(value: T): T {
  return structuredClone(value);
}
export function isCommandFinal(command: Command | null | undefined): boolean {
  return (
    !command ||
    ["succeeded", "failed", "expired", "cancelled", "interrupted"].includes(
      command.state,
    )
  );
}
export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days
    ? `${days} д ${hours} ч`
    : hours
      ? `${hours} ч ${minutes} мин`
      : `${minutes} мин`;
}
