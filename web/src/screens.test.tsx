import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DevicePage, UpdatesPage } from "./device-pages";
import { NetworkPage, ServicesPage } from "./service-pages";
import { PhotosPage, ScreenPage } from "./screen-photo-pages";
import type { PageProps } from "./page-types";
import type { Envelope, Settings } from "./types";

const initial: Settings = {
  screen: {
    name: "HomeDash",
    city: "Belgrade",
    timezone: "Europe/Belgrade",
    clock_format: "24h",
    idle_action: "photos",
    night_enabled: false,
    night_start: "23:00",
    night_end: "07:00",
    auto_update_check: true,
    auto_install: false,
  },
  photos: {
    enabled: true,
    source: "immich",
    base_url: "https://photos.example",
    album_id: "",
    idle_seconds: 300,
    interval_seconds: 60,
    order: "shuffle",
    layout: "adaptive",
    crop_limit_percent: 10,
  },
  services: {
    weather: {
      enabled: true,
      latitude: 44.82,
      longitude: 20.46,
      timezone: "Europe/Belgrade",
    },
    calendar: {
      enabled: true,
      base_url: "",
      ids: [],
      timezone: "Europe/Belgrade",
    },
    transport: { enabled: true, base_url: "", ids: [] },
    health: { enabled: false, base_url: "" },
    tasks: { enabled: false },
    news: { enabled: false, base_url: "", limit: 5, since_hours: 24 },
    home: {
      enabled: false,
      base_url: "",
      sensors: [],
      lights: [],
      climates: [],
    },
  },
  network: { bridge_url: "" },
};
const envelope: Envelope = {
  device_id: "a".repeat(32),
  revision: 3,
  settings: initial,
  secrets_present: {
    health_api_key: false,
    todoist_token: false,
    ha_token: false,
    calendar_platform_token: false,
    immich_api_key: false,
  },
  device: {
    connected: true,
    last_seen: null,
    applied_revision: 3,
    firmware: "1.2.0",
    ip: "192.0.2.3",
    ssid: "Home Wi-Fi",
    uptime_seconds: 60,
    time_synced: true,
    capabilities: [
      "preferences",
      "photo_start",
      "restart",
      "ota_check",
      "ota_install",
      "reset",
      "network",
    ],
    error: "",
    command: null,
  },
  statuses: {},
  capabilities: {
    brightness: false,
    google_photos: false,
    timezones: ["Europe/Belgrade", "UTC"],
  },
};

function pageProps(): PageProps {
  return {
    draft: structuredClone(initial),
    edit: vi.fn(),
    save: vi.fn().mockResolvedValue(null),
    dirty: false,
    envelope,
    deviceId: "a".repeat(32),
    onOpenConnections: vi.fn(),
    setServiceDetail: vi.fn(),
    serviceDetail: "weather",
    setNetworkCredentials: vi.fn(),
    networkCredentials: {},
    networkEditing: true,
    setNetworkEditing: vi.fn(),
    updates: null,
    command: null,
    pendingCommand: false,
    ask: vi.fn(),
    busy: false,
    identity: { method: "password" },
    refreshStatus: vi.fn(),
    downloadDiagnostics: vi.fn(),
    runAction: vi.fn(),
  };
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("settings screen field interactions", () => {
  it("edits the display name", () => {
    const props = pageProps();
    render(<ScreenPage {...props} />);
    fireEvent.change(screen.getByLabelText("Имя дисплея"), {
      target: { value: "Living room" },
    });
    expect(props.edit).toHaveBeenCalledWith(
      expect.objectContaining({
        screen: expect.objectContaining({ name: "Living room" }),
      }),
    );
  });

  it("edits the Immich album ID without exposing an album picker", () => {
    const props = pageProps();
    render(<PhotosPage {...props} />);
    fireEvent.change(screen.getByLabelText("Album ID"), {
      target: { value: "album-uuid" },
    });
    expect(props.edit).toHaveBeenCalledWith(
      expect.objectContaining({
        photos: expect.objectContaining({ album_id: "album-uuid" }),
      }),
    );
  });

  it("keeps photo start unavailable when the frame has no enabled source", () => {
    const props = pageProps();
    props.draft.photos.source = "none";
    render(<PhotosPage {...props} />);
    expect(screen.getByRole("button", { name: /Запустить на дисплее/ })).toHaveProperty("disabled", true);
    expect(screen.getByText(/выберите источник/)).toBeTruthy();
  });

  it("keeps photo source settings beside the placement preview", () => {
    const props = pageProps();
    const { container } = render(<PhotosPage {...props} />);
    const firstRegion = container.querySelector(".photos-source-preview");
    expect(firstRegion?.querySelector(".photos-source-column")).not.toBeNull();
    expect(firstRegion?.querySelector(".preview-wrap .photo-preview")).not.toBeNull();
    expect(screen.getByText("Google", { selector: ".source-wordmark" })).toBeTruthy();
  });

  it("labels the photo-frame switch with its current state", () => {
    const props = pageProps();
    props.draft.photos.enabled = false;
    render(<PhotosPage {...props} />);
    fireEvent.click(screen.getByRole("switch", { name: "Фоторамка выключена" }));
    expect(props.edit).toHaveBeenCalledWith(
      expect.objectContaining({
        photos: expect.objectContaining({ enabled: true }),
      }),
    );
  });

  it("labels the night-mode switch with its current state", () => {
    const props = pageProps();
    render(<ScreenPage {...props} />);
    expect(
      screen.getByRole("switch", { name: "Ночной режим выключен" }),
    ).toBeTruthy();
  });

  it("selects a service and opens shared connection settings for its source", () => {
    const props = pageProps();
    const view = render(<ServicesPage {...props} />);
    fireEvent.click(screen.getByRole("button", { name: /Календарь/ }));
    view.rerender(<ServicesPage {...props} serviceDetail="calendar" />);
    fireEvent.click(screen.getByRole("button", { name: "Общие подключения" }));
    expect(props.setServiceDetail).toHaveBeenCalledWith("calendar");
    expect(props.onOpenConnections).toHaveBeenCalledOnce();
  });

  it("opens the mobile service sheet on selection and returns focus on Escape", () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({ matches: true }),
    );
    const props = pageProps();
    const view = render(<ServicesPage {...props} />);
    expect(screen.queryByRole("dialog")).toBeNull();

    const calendar = screen.getByRole("button", {
      name: /КалендарьCalendar Platform/,
    });
    calendar.focus();
    fireEvent.click(calendar);
    view.rerender(<ServicesPage {...props} serviceDetail="calendar" />);

    expect(screen.getByRole("dialog", { name: "Календарь" })).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(calendar);
  });

  it("gives the weather timezone control an accessible name", () => {
    const props = pageProps();
    render(<ServicesPage {...props} />);
    expect(screen.getByLabelText("Часовой пояс погоды")).toBeTruthy();
  });

  it("saves service selection from the mobile sheet through the global callback", async () => {
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({ matches: true }),
    );
    const props = pageProps();
    const view = render(<ServicesPage {...props} />);
    fireEvent.click(
      screen.getByRole("button", {
        name: /КалендарьCalendar Platform/,
      }),
    );
    view.rerender(<ServicesPage {...props} serviceDetail="calendar" />);

    const dialog = screen.getByRole("dialog", { name: "Календарь" });
    fireEvent.change(within(dialog).getByLabelText("Календари"), {
      target: { value: "primary-calendar" },
    });
    expect(props.edit).toHaveBeenCalledWith(expect.objectContaining({
      services: expect.objectContaining({
        calendar: expect.objectContaining({ ids: ["primary-calendar"] }),
      }),
    }));
    props.dirty = true;
    view.rerender(<ServicesPage {...props} serviceDetail="calendar" />);
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "Календарь" })).getByRole(
        "button",
        { name: "Сохранить изменения" },
      ),
    );

    await waitFor(() => expect(props.save).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("edits network credentials only while the network form is open", () => {
    const props = pageProps();
    render(<NetworkPage {...props} />);
    fireEvent.change(screen.getByLabelText("Новая сеть Wi-Fi"), {
      target: { value: "New SSID" },
    });
    fireEvent.change(screen.getByLabelText("Адрес Bridge"), {
      target: { value: "https://bridge.example" },
    });
    expect(props.setNetworkCredentials).toHaveBeenCalledWith(
      expect.any(Function),
    );
    const updateCredentials = vi.mocked(props.setNetworkCredentials).mock
      .calls[0][0] as (state: Record<string, string>) => Record<string, string>;
    expect(updateCredentials({})).toEqual({ wifi_ssid: "New SSID" });
    expect(props.edit).toHaveBeenCalledWith(
      expect.objectContaining({
        network: { bridge_url: "https://bridge.example" },
      }),
    );
  });

  it("does not display an unreported applied revision as device status", () => {
    const props = pageProps();
    props.envelope.device.last_seen = null;
    render(<NetworkPage {...props} />);
    expect(screen.getByText("Подтверждение не получено")).toBeTruthy();
    expect(screen.queryByText(/ревизия/i)).toBeNull();
  });

  it("edits automatic update checks", () => {
    const props = pageProps();
    render(<UpdatesPage {...props} />);
    fireEvent.click(
      screen.getByRole("switch", {
        name: "Автоматически проверять обновления",
      }),
    );
    expect(props.edit).toHaveBeenCalledWith(
      expect.objectContaining({
        screen: expect.objectContaining({ auto_update_check: false }),
      }),
    );
  });

  it("requests the supported restart command from the device screen", () => {
    const props = pageProps();
    render(<DevicePage {...props} />);
    const button = screen.getByRole("button", {
      name: /Перезапустить дисплей/,
    });
    expect((button as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(button);
    expect(props.ask).toHaveBeenCalledWith(
      expect.objectContaining({ action: "restart" }),
    );
    expect(
      screen.getByText("Запуск фоторамки", { selector: ".badge" }),
    ).toBeTruthy();
    expect(screen.queryByText("photo_start")).toBeNull();
  });

  it("does not present unreported device uptime or revision zero as state", () => {
    const props = pageProps();
    props.envelope.device.connected = false;
    props.envelope.device.last_seen = null;
    props.envelope.device.uptime_seconds = 0;
    props.envelope.device.applied_revision = 0;
    const { container } = render(<DevicePage {...props} />);
    expect(
      screen.getByText("Время работы").parentElement?.querySelector("strong")
        ?.textContent,
    ).toBe("Не сообщается");
    const settingsRow = [
      ...container.querySelectorAll(".device-facts > div"),
    ].find((row) => row.querySelector("span")?.textContent === "Настройки");
    expect(
      settingsRow?.querySelector("strong")?.textContent,
    ).toBe("Ожидает подключения");
    expect(screen.queryByText(/ревизия 0/i)).toBeNull();
  });
});
