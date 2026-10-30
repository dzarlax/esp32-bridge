import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const firstId = "a".repeat(32);
const secondId = "b".repeat(32);

function settingsEnvelope(deviceId: string, name: string) {
  return {
    device_id: deviceId,
    revision: 2,
    settings: {
      screen: { name, city: "Belgrade", timezone: "Europe/Belgrade", clock_format: "24h", idle_action: "photos", night_enabled: false, night_start: "23:00", night_end: "07:00", auto_update_check: true, auto_install: false },
      photos: { enabled: true, source: "none", base_url: "", album_id: "", idle_seconds: 300, interval_seconds: 60, order: "shuffle", layout: "adaptive", crop_limit_percent: 10 },
      services: {
        weather: { enabled: true, latitude: 44.8, longitude: 20.4, timezone: "Europe/Belgrade" },
        calendar: { enabled: false, base_url: "", ids: [], timezone: "Europe/Belgrade" },
        transport: { enabled: false, base_url: "", ids: [] },
        health: { enabled: false, base_url: "" }, tasks: { enabled: false },
        news: { enabled: false, base_url: "", limit: 5, since_hours: 24 },
        home: { enabled: false, base_url: "", sensors: [], lights: [], climates: [] },
      },
      network: { bridge_url: "" },
    },
    secrets_present: { health_api_key: false, todoist_token: false, ha_token: false, calendar_platform_token: false, immich_api_key: false },
    device: { connected: true, last_seen: new Date().toISOString(), applied_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test", uptime_seconds: 120, time_synced: true, capabilities: ["preferences", "network", "restart"], error: "", command: null },
    statuses: {},
    capabilities: { brightness: false, google_photos: false, timezones: ["Europe/Belgrade", "UTC"], locales: ["ru", "en", "sr-Latn"] },
  };
}

const connections = {
  revision: 8,
  connections: { immich: { base_url: "" }, health: { base_url: "" }, tasks: {}, news: { base_url: "" }, home: { base_url: "" }, calendar: { base_url: "" }, transport: { base_url: "" } },
  secrets_present: { health_api_key: false, todoist_token: false, ha_token: false, calendar_platform_token: false, immich_api_key: false },
};

function json(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });
}

function installFetch(devices: { id: string; name: string }[], settings: (id: string) => Promise<unknown>) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
    const path = String(input);
    if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
    if (path.endsWith("/devices")) return json({ devices: devices.map(({ id, name }) => ({ id, name, connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" })), max_devices: 8 });
    if (path.endsWith("/connections")) return json(connections);
    if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
    const match = path.match(/\/devices\/([a-f0-9]{32})\/settings$/);
    if (match) return json(await settings(match[1]));
    return json({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.localStorage.clear(); });

describe("multi-display shell", () => {
  it("switches the interface language without losing a draft or validation error", async () => {
    installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    render(<App />);
    const city = await screen.findByLabelText("Подпись города") as HTMLInputElement;
    fireEvent.change(city, { target: { value: "Unsubmitted city" } });
    const idle = screen.getByLabelText("Задержка бездействия");
    fireEvent.change(idle, { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    expect(await screen.findAllByText(/Введите целое число от 30/)).toHaveLength(2);
    fireEvent.change(screen.getByLabelText("Выберите язык интерфейса"), { target: { value: "en" } });
    expect((await screen.findAllByText("Screen")).length).toBeGreaterThan(0);
    expect((screen.getByLabelText("City label") as HTMLInputElement).value).toBe("Unsubmitted city");
    expect((screen.getByLabelText("Idle delay") as HTMLInputElement).value).toBe("5");
    expect(screen.getByText("Enter a whole number from 30 to 3600 sec.")).toBeTruthy();
    expect(window.localStorage.getItem("homedash.ui.locale")).toBe("en");
    expect(document.documentElement.lang).toBe("en");
    expect(document.title).toBe("HomeDash — Settings");
  });

  it("keeps the session language switch usable when local storage is blocked", async () => {
    installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => { throw new DOMException("Blocked", "SecurityError"); });
    render(<App />);
    await screen.findByLabelText("Подпись города");
    fireEvent.change(screen.getByLabelText("Выберите язык интерфейса"), { target: { value: "sr-Latn" } });
    expect((await screen.findAllByText("Ekran")).length).toBeGreaterThan(0);
    expect(document.documentElement.lang).toBe("sr-Latn");
    expect(document.title).toBe("HomeDash — Podešavanja");
  });

  it("retranslates a visible login error after switching languages", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: false, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/login")) return new Response(JSON.stringify({ error: "invalid_password" }), { status: 401, headers: { "Content-Type": "application/json" } });
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Выберите язык интерфейса"), { target: { value: "en" } });
    fireEvent.change(screen.getByLabelText("Administrator password"), { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Incorrect password. Check it and try again.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Interface language"), { target: { value: "sr-Latn" } });
    expect(await screen.findByText("Lozinka nije tačna. Proverite je i pokušajte ponovo.")).toBeTruthy();
  });

  it("defaults an absent screen language to Russian and disables unsupported device language", async () => {
    installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    render(<App />);
    const displayLanguage = await screen.findByLabelText("Язык дисплея") as HTMLSelectElement;
    expect(displayLanguage.value).toBe("ru");
    expect(displayLanguage.disabled).toBe(true);
    expect(screen.getByText("Язык дисплея не поддерживается этой прошивкой.")).toBeTruthy();
  });

  it("saves a device screen language only when firmware reports locale support", async () => {
    const fetchMock = installFetch([{ id: firstId, name: "Hall" }], async (id) => {
      const envelope = settingsEnvelope(id, "Hall");
      envelope.device.capabilities.push("locale");
      Object.assign(envelope.device, { applied_locale: "sr-Latn" });
      return envelope;
    });
    render(<App />);
    const displayLanguage = await screen.findByLabelText("Язык дисплея");
    expect((displayLanguage as HTMLSelectElement).disabled).toBe(false);
    expect(screen.getAllByRole("option", { name: "Srpski (latinica)" })).toHaveLength(2);
    expect(screen.getByText("Применённый язык: Srpski (latinica)")).toBeTruthy();
    fireEvent.change(displayLanguage, { target: { value: "en" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));
    const [, init] = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
    expect(JSON.parse(String(init?.body)).settings.screen.locale).toBe("en");
  });

  it("explains the separate minimum for photo changes", async () => {
    const fetchMock = installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    render(<App />);
    await screen.findByLabelText("Задержка бездействия");
    fireEvent.click(screen.getByRole("button", { name: "Фоторамка" }));
    const input = await screen.findByLabelText("Интервал смены фотографии");
    fireEvent.change(input, { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(document.activeElement).toBe(input));
    expect(screen.getByText("Введите целое число от 15 до 3600 сек.")).toBeTruthy();
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(0);
  });

  it("rejects a five-second idle delay locally and saves after correction", async () => {
    const fetchMock = installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    render(<App />);
    const input = await screen.findByLabelText("Задержка бездействия");
    fireEvent.change(input, { target: { value: "5" } });
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText("Введите целое число от 30 до 3600 сек.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(document.activeElement).toBe(input));
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(0);
    expect(screen.getByRole("alert").textContent).toContain("Запуск после бездействия");
    fireEvent.change(input, { target: { value: "30" } });
    expect(input.getAttribute("aria-invalid")).toBe("false");
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1));
    const [, init] = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
    expect(JSON.parse(String(init?.body)).settings.photos.idle_seconds).toBe(30);
  });

  it("shows setup instructions and shared connections when no display is registered", async () => {
    installFetch([], async () => ({}));
    render(<App />);
    expect(await screen.findByText("Дисплеи ещё не подключены")).toBeTruthy();
    expect(screen.queryByLabelText("Выбрать дисплей")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Общие подключения" }));
    expect(await screen.findByText("Адреса источников")).toBeTruthy();
    expect(screen.getByText("Параметры источников для всех дисплеев")).toBeTruthy();
  });

  it("keeps the dirty draft when a profile switch is declined", async () => {
    installFetch([{ id: firstId, name: "Kitchen" }, { id: secondId, name: "Office" }], async (id) => settingsEnvelope(id, id === firstId ? "Kitchen" : "Office"));
    vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<App />);
    const name = await screen.findByLabelText("Имя дисплея") as HTMLInputElement;
    fireEvent.change(name, { target: { value: "Unsaved kitchen" } });
    fireEvent.change(screen.getByLabelText("Выбрать дисплей"), { target: { value: secondId } });
    expect((screen.getByLabelText("Выбрать дисплей") as HTMLSelectElement).value).toBe(firstId);
    expect((screen.getByLabelText("Имя дисплея") as HTMLInputElement).value).toBe("Unsaved kitchen");
  });

  it("asks before discarding a dirty draft when switching displays", async () => {
    installFetch([{ id: firstId, name: "Kitchen" }, { id: secondId, name: "Office" }], async (id) => settingsEnvelope(id, id === firstId ? "Kitchen" : "Office"));
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Имя дисплея"), { target: { value: "Unsaved kitchen" } });
    fireEvent.change(screen.getByLabelText("Выбрать дисплей"), { target: { value: secondId } });
    expect(confirm).toHaveBeenCalledOnce();
    expect(await screen.findByDisplayValue("Office")).toBeTruthy();
  });

  it("ignores a late settings response from a previously selected display", async () => {
    let releaseFirst!: (value: unknown) => void;
    const firstResponse = new Promise((resolve) => { releaseFirst = resolve; });
    installFetch([{ id: firstId, name: "Kitchen" }, { id: secondId, name: "Office" }], async (id) => id === firstId ? firstResponse : settingsEnvelope(secondId, "Office"));
    render(<App />);
    await waitFor(() => expect(screen.getByLabelText("Выбрать дисплей")).toBeTruthy());
    fireEvent.change(screen.getByLabelText("Выбрать дисплей"), { target: { value: secondId } });
    expect(await screen.findByDisplayValue("Office")).toBeTruthy();
    releaseFirst(settingsEnvelope(firstId, "Kitchen"));
    await waitFor(() => expect((screen.getByLabelText("Имя дисплея") as HTMLInputElement).value).toBe("Office"));
  });
});
