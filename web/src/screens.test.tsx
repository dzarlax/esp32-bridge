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
import { LocaleProvider } from "./i18n";

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
    envelope: structuredClone(envelope),
    deviceId: "a".repeat(32),
    onOpenConnections: vi.fn(),
    setServiceDetail: vi.fn(),
    serviceDetail: "weather",
    setNetworkCredentials: vi.fn(),
    networkCredentials: {},
    networkEditing: true,
    setNetworkEditing: vi.fn(),
    updates: null,
    firmwareObservationFresh: false,
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
  window.localStorage.clear();
});

describe("settings screen field interactions", () => {
  it.each([
    ["en", "Not reported", "No connection", "Offline"],
    ["sr-Latn", "Nije prijavljeno", "Nema veze", "Van mreže"],
  ] as const)("localizes empty network values and offline statuses in %s", (locale, empty, noConnection, offline) => {
    const props = pageProps();
    props.envelope.device.connected = false;
    props.envelope.device.ssid = "";
    props.envelope.device.ip = "";
    window.localStorage.setItem("homedash.ui.locale", locale);
    render(<LocaleProvider><NetworkPage {...props} /></LocaleProvider>);
    expect(screen.getAllByText(empty)).toHaveLength(2);
    expect(screen.getByText(noConnection)).toBeTruthy();
    expect(screen.getByText(offline)).toBeTruthy();
  });

  it.each([
    ["en", "Interrupted by restart"],
    ["sr-Latn", "Prekinuto pri ponovnom pokretanju"],
  ] as const)("localizes a rendered command status in %s", (locale, status) => {
    const props = pageProps();
    props.command = { id: "command-1", action: "restart", state: "interrupted", created_at: "2026-10-30T10:00:00Z", expires_at: "2026-10-30T10:05:00Z" };
    window.localStorage.setItem("homedash.ui.locale", locale);
    render(<LocaleProvider><UpdatesPage {...props} /></LocaleProvider>);
    expect(screen.getByText(status)).toBeTruthy();
  });

  it.each([
    ["en", "Calendar Platform, read only"],
    ["sr-Latn", "Calendar Platform, samo za čitanje"],
  ] as const)("localizes the shared-connection accessible name in %s", (locale, label) => {
    const props = pageProps();
    props.serviceDetail = "calendar";
    window.localStorage.setItem("homedash.ui.locale", locale);
    render(<LocaleProvider><ServicesPage {...props} /></LocaleProvider>);
    expect(screen.getByLabelText(label)).toBeTruthy();
  });

  it.each([
    ["en", "Shared Immich server, read only", "Not configured"],
    ["sr-Latn", "Zajednički Immich server, samo za čitanje", "Nije podešeno"],
  ] as const)("localizes the empty shared Immich server field in %s", (locale, label, value) => {
    const props = pageProps();
    props.draft.photos.source = "immich";
    props.draft.photos.base_url = "";
    window.localStorage.setItem("homedash.ui.locale", locale);
    render(<LocaleProvider><PhotosPage {...props} /></LocaleProvider>);
    const input = screen.getByLabelText(label) as HTMLInputElement;
    expect(input.value).toBe(value);
  });

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

  it("explains adaptive firmware compatibility while preserving editable preferences", () => {
    const props = pageProps();
    props.envelope.device.capabilities = ["preferences", "photo_overlay"];
    const view = render(<PhotosPage {...props} />);
    expect(screen.getByText("Адаптивное оформление появится после обновления прошивки дисплея.")).toBeTruthy();
    const weather = screen.getByRole("switch", { name: /^Погода$/ }) as HTMLInputElement;
    expect(weather.disabled).toBe(false);
    fireEvent.click(weather);
    expect(props.edit).toHaveBeenCalledWith(expect.objectContaining({
      photos: expect.objectContaining({ show_weather: false }),
    }));
    props.envelope.device.capabilities.push("photo_overlay_adaptive");
    view.rerender(<PhotosPage {...props} />);
    expect(screen.queryByText("Адаптивное оформление появится после обновления прошивки дисплея.")).toBeNull();
    expect(screen.getByText("Постоянный информационный слой появится после обновления прошивки дисплея.")).toBeTruthy();
    props.envelope.device.capabilities.push("photo_scene_unified");
    view.rerender(<PhotosPage {...props} />);
    expect(screen.queryByText("Постоянный информационный слой появится после обновления прошивки дисплея.")).toBeNull();
    expect(view.container.querySelector(".photo-preview-overlays.unified")).not.toBeNull();
    expect(view.container.querySelector(".photo-preview-overlays.adaptive")).toBeNull();
  });

  it("defaults absent photo overlay settings to enabled and edits each toggle independently", () => {
    const props = pageProps();
    delete props.draft.photos.show_clock;
    delete props.draft.photos.show_weather;
    delete props.draft.photos.show_event;
    render(<PhotosPage {...props} />);

    const clock = screen.getByRole("switch", { name: "Часы и дата" }) as HTMLInputElement;
    const weather = screen.getByRole("switch", { name: "Погода" }) as HTMLInputElement;
    const event = screen.getByRole("switch", { name: "Ближайшее событие" }) as HTMLInputElement;
    expect(clock.checked).toBe(true);
    expect(weather.checked).toBe(true);
    expect(event.checked).toBe(true);

    fireEvent.click(weather);
    expect(props.edit).toHaveBeenCalledWith(expect.objectContaining({
      photos: expect.objectContaining({ show_weather: false }),
    }));
    expect(props.edit).not.toHaveBeenCalledWith(expect.objectContaining({
      photos: expect.objectContaining({ show_clock: false }),
    }));
    expect(clock.checked).toBe(true);
    expect(event.checked).toBe(true);
  });

  it.each([
    ["en", "Clock and date", "Weather", "Next event"],
    ["sr-Latn", "Sat i datum", "Vreme", "Sledeći događaj"],
  ] as const)("localizes photo overlay controls in %s", (locale, clock, weather, event) => {
    const props = pageProps();
    window.localStorage.setItem("homedash.ui.locale", locale);
    render(<LocaleProvider><PhotosPage {...props} /></LocaleProvider>);
    expect(screen.getByRole("switch", { name: clock })).toBeTruthy();
    expect(screen.getByRole("switch", { name: weather })).toBeTruthy();
    expect(screen.getByRole("switch", { name: event })).toBeTruthy();
  });

  it("gates brightness and sends a bounded preview without editing settings", () => {
    const props = pageProps();
    props.envelope.device.capabilities.push("backlight_preview", "backlight_restore");
    render(<ScreenPage {...props} />);
    expect((screen.getByLabelText("Дневная яркость") as HTMLInputElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Яркость проверки"), { target: { value: "30" } });
    fireEvent.click(screen.getByRole("button", { name: "Проверить на 20 секунд" }));
    expect(props.runAction).toHaveBeenCalledWith("backlight_preview", 30);
    expect(props.edit).not.toHaveBeenCalled();
  });

  it("reorders configured favorites with keyboard-accessible buttons", () => {
    const props = pageProps();
    props.serviceDetail = "home";
    props.envelope.device.capabilities.push("home_favorites");
    props.draft.services.home.lights = ["light.a", "light.b"];
    props.draft.services.home.favorites = [{ entity_id: "light.a", kind: "light" }, { entity_id: "light.b", kind: "light" }];
    render(<ServicesPage {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Переместить выше light.b" }));
    expect(props.edit).toHaveBeenCalledWith(expect.objectContaining({ services: expect.objectContaining({ home: expect.objectContaining({ favorites: [{ entity_id: "light.b", kind: "light" }, { entity_id: "light.a", kind: "light" }] }) }) }));
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
