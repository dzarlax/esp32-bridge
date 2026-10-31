import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.useRealTimers(); window.localStorage.clear(); });

describe("multi-display shell", () => {
  it("drops a current-version claim when an updates poll fails despite retained online flags", async () => {
    vi.useFakeTimers();
    let failUpdates = false;
    let mismatchSnapshots = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.2.3", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) {
        if (failUpdates) return new Response(JSON.stringify({ error: "catalog_upstream_error" }), { status: 502 });
        return json({ current_version: "1.2.3", available_version: "1.2.3", update_available: true, device_connected: true, history: [{ id: "attempt", version: "1.2.3", state: "unknown", at: new Date().toISOString() }] });
      }
      const match = path.match(/\/devices\/([a-f0-9]{32})\/settings$/);
      if (match) {
        const envelope = settingsEnvelope(match[1]!, "Hall");
        envelope.device.firmware = mismatchSnapshots ? "1.2.2" : "1.2.3";
        envelope.device.capabilities.push("ota_install");
        return json(envelope);
      }
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
    fireEvent.click(screen.getByRole("button", { name: "Обновления" }));
    expect(screen.getByText("Доступное обновление уже установлено")).toBeTruthy();
    expect(screen.getByText("Версия подтверждена текущим ответом дисплея")).toBeTruthy();

    mismatchSnapshots = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(8000); for (let i = 0; i < 12; i++) await Promise.resolve(); });
    expect(screen.queryByText("Доступное обновление уже установлено")).toBeNull();
    expect(screen.queryByText("Версия подтверждена текущим ответом дисплея")).toBeNull();
    expect(screen.getByText("Последние ответы Bridge расходятся по версии")).toBeTruthy();
    expect(screen.queryByText("Доступна версия")).toBeNull();

    failUpdates = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(8000); for (let i = 0; i < 12; i++) await Promise.resolve(); });
    expect(screen.queryByText("Доступное обновление уже установлено")).toBeNull();
    expect(screen.queryByText("Версия подтверждена текущим ответом дисплея")).toBeNull();
    expect(screen.getByText("Показана последняя известная версия; актуальность не подтверждена")).toBeTruthy();
    expect(screen.getByText("Доступна версия")).toBeTruthy();
  });

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

  it("persists independently edited photo overlay settings in the settings payload", async () => {
    const fetchMock = installFetch([{ id: firstId, name: "Hall" }], async (id) => settingsEnvelope(id, "Hall"));
    render(<App />);
    await screen.findByLabelText("Подпись города");
    fireEvent.click(screen.getByRole("button", { name: "Фоторамка" }));

    fireEvent.click(screen.getByRole("switch", { name: "Часы и дата" }));
    fireEvent.click(screen.getByRole("switch", { name: "Ближайшее событие" }));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));

    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));
    const [, init] = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
    const saved = JSON.parse(String(init?.body)).settings.photos;
    expect(saved.show_clock).toBe(false);
    expect(saved.show_event).toBe(false);
    expect(saved.show_weather).toBeUndefined();
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

  it("keeps a lost PATCH uncertain after an unchanged read, then reconciles a delayed commit without resaving", async () => {
    let serverEnvelope = settingsEnvelope(firstId, "Hall");
    let patchBody: any;
    let losePatch!: () => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: serverEnvelope.device.connected, last_seen: serverEnvelope.device.last_seen, applied_revision: serverEnvelope.device.applied_revision, desired_revision: serverEnvelope.revision, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH") {
        patchBody = JSON.parse(String(init.body));
        return new Promise((_resolve, reject) => { losePatch = () => reject(new Error("connection lost")); });
      }
      if (path.endsWith(`/devices/${firstId}/settings`)) return json(structuredClone(serverEnvelope));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    const city = await screen.findByLabelText("Подпись города");
    fireEvent.change(city, { target: { value: "Novi Sad" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(losePatch).toBeTypeOf("function"));
    losePatch();
    expect(await screen.findByText("Результат сохранения не подтверждён")).toBeTruthy();
    expect(screen.getByText("Проверяется текущая версия настроек")).toBeTruthy();
    expect(screen.queryByText("Настройки подтверждены устройством")).toBeNull();
    expect((screen.getByLabelText("Подпись города") as HTMLInputElement).value).toBe("Novi Sad");

    // The first read saw the unchanged base. The server can still finish the PATCH later.
    serverEnvelope = structuredClone(serverEnvelope);
    serverEnvelope.revision = 3;
    serverEnvelope.settings = patchBody.settings;
    serverEnvelope.device.applied_revision = 2;
    fireEvent.click(screen.getByRole("button", { name: "Проверить статус" }));
    expect(await screen.findByText("Сохранено в Bridge")).toBeTruthy();
    expect(screen.getByText("Ожидает применения дисплеем")).toBeTruthy();
    expect((screen.getByLabelText("Подпись города") as HTMLInputElement).value).toBe("Novi Sad");
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
  });

  it("ignores an older status read that returns after a successful save", async () => {
    let serverEnvelope = settingsEnvelope(firstId, "Hall");
    let deferNextRead = false;
    let releaseOldRead!: (response: Response) => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: serverEnvelope.device.applied_revision, desired_revision: serverEnvelope.revision, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body));
        serverEnvelope = structuredClone(serverEnvelope);
        serverEnvelope.revision = 3;
        serverEnvelope.settings = body.settings;
        serverEnvelope.device.applied_revision = 2;
        return json(structuredClone(serverEnvelope));
      }
      if (path.endsWith(`/devices/${firstId}/settings`)) {
        if (deferNextRead) {
          deferNextRead = false;
          return new Promise<Response>((resolve) => { releaseOldRead = resolve; });
        }
        return json(structuredClone(serverEnvelope));
      }
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<App />);
    await screen.findByLabelText("Подпись города");
    fireEvent.click(screen.getByRole("button", { name: "Об устройстве" }));
    deferNextRead = true;
    fireEvent.click(screen.getByRole("button", { name: "Обновить статус" }));
    await waitFor(() => expect(releaseOldRead).toBeTypeOf("function"));

    fireEvent.click(screen.getByRole("button", { name: "Экран" }));
    fireEvent.change(await screen.findByLabelText("Подпись города"), { target: { value: "Novi Sad" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    expect(await screen.findByText("Сохранено в Bridge")).toBeTruthy();
    expect(screen.getByText("Ожидает применения дисплеем")).toBeTruthy();

    releaseOldRead(json(settingsEnvelope(firstId, "Hall")));
    await waitFor(() => expect(screen.getByText("Ожидает применения дисплеем")).toBeTruthy());
    expect((screen.getByLabelText("Подпись города") as HTMLInputElement).value).toBe("Novi Sad");
  });

  it("does not infer success or failure for a write-only key from unchanged settings", async () => {
    const serverEnvelope = settingsEnvelope(firstId, "Hall");
    let losePatch!: () => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH")
        return new Promise((_resolve, reject) => { losePatch = () => reject(new Error("connection lost")); });
      if (path.endsWith(`/devices/${firstId}/settings`)) return json(structuredClone(serverEnvelope));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await screen.findByLabelText("Подпись города");
    fireEvent.click(screen.getByRole("button", { name: "Сеть и Bridge" }));
    fireEvent.click(await screen.findByRole("button", { name: "Изменить сеть" }));
    fireEvent.change(screen.getByLabelText("Пароль Wi-Fi"), { target: { value: "write-only-test" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(losePatch).toBeTypeOf("function"));
    losePatch();
    expect(await screen.findByText("Результат сохранения не подтверждён")).toBeTruthy();
    expect(screen.getByText(/Записанные ключи нельзя проверить через настройки/)).toBeTruthy();
    expect(screen.queryByText("Bridge не сохранил изменения. Ваш черновик остался в форме.")).toBeNull();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
  });

  it("retains the last status after a failed GET and lets the user check again", async () => {
    let failNextSettingsRead = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      void init;
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && failNextSettingsRead) {
        failNextSettingsRead = false;
        return new Response(JSON.stringify({ error: "temporary_failure" }), { status: 503, headers: { "Content-Type": "application/json" } });
      }
      if (path.endsWith(`/devices/${firstId}/settings`)) return json(settingsEnvelope(firstId, "Hall"));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await screen.findByLabelText("Подпись города");
    fireEvent.click(screen.getByRole("button", { name: "Об устройстве" }));
    failNextSettingsRead = true;
    fireEvent.click(screen.getByRole("button", { name: "Обновить статус" }));
    expect(await screen.findByText("Не удалось обновить статус. Показано последнее известное состояние.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Проверить статус" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Проверить статус" }));
    await waitFor(() => expect(screen.queryByText("Не удалось обновить статус. Показано последнее известное состояние.")).toBeNull());
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(0);
    expect(fetchMock.mock.calls.some(([path, init]) => String(path).endsWith(`/devices/${firstId}/settings`) && (init?.method ?? "GET") === "GET")).toBe(true);
  });

  it("withdraws backlight confirmation immediately after a failed settings poll", async () => {
    let fail = false;
    const fetchMock = installFetch([{ id: firstId, name: "Hall" }], async id => {
      if (fail) throw new Error("offline");
      return { ...settingsEnvelope(id, "Hall"), device: { ...settingsEnvelope(id, "Hall").device,
        backlight_report: { version: 1, phase: "restored", output_known: true, last_command: 50,
          test_command: 70, return_command: 50, sample_uptime_ms: 30000, restore_deadline_uptime_ms: 30000, error: "" } } };
    });
    render(<App />);
    await screen.findByText("Команда возврата выполнена");
    fireEvent.click(screen.getByRole("button", { name: "Об устройстве" }));
    fail = true;
    fireEvent.click(screen.getByRole("button", { name: "Обновить статус" }));
    await screen.findByText("Не удалось обновить статус. Показано последнее известное состояние.");
    fireEvent.click(screen.getByRole("button", { name: "Экран" }));
    expect(screen.getByText("Нет свежего подтверждения подсветки")).toBeTruthy();
    expect(screen.queryByText("Команда возврата выполнена")).toBeNull();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(0);
  });

  it.each([
    [409, "revision_conflict"],
    [422, "invalid_settings"],
    [503, "settings_unavailable"],
  ] as const)("keeps the user's draft after a %i settings rejection", async (status, error) => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH")
        return new Response(JSON.stringify({ error }), { status, headers: { "Content-Type": "application/json" } });
      if (path.endsWith(`/devices/${firstId}/settings`)) {
        const latest = settingsEnvelope(firstId, "Hall");
        if (status === 409) latest.revision = 3;
        return json(latest);
      }
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    const city = await screen.findByLabelText("Подпись города");
    fireEvent.change(city, { target: { value: "Draft city" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1));
    expect((screen.getByLabelText("Подпись города") as HTMLInputElement).value).toBe("Draft city");
    expect(screen.getByText("Есть несохранённые изменения")).toBeTruthy();
  });

  it("reconciles a late matching commit from background polling without a conflict or duplicate PATCH", async () => {
    vi.useFakeTimers();
    let serverEnvelope = settingsEnvelope(firstId, "Hall");
    let patchBody: any;
    let losePatch!: () => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: serverEnvelope.device.applied_revision, desired_revision: serverEnvelope.revision, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH") {
        patchBody = JSON.parse(String(init.body));
        return new Promise((_resolve, reject) => { losePatch = () => reject(new Error("connection lost")); });
      }
      if (path.endsWith(`/devices/${firstId}/settings`)) return json(structuredClone(serverEnvelope));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await act(async () => { for (let i = 0; i < 40; i++) await Promise.resolve(); });
    const city = screen.getByLabelText("Подпись города");
    fireEvent.change(city, { target: { value: "Novi Sad" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });
    expect(losePatch).toBeTypeOf("function");
    await act(async () => { losePatch(); for (let i = 0; i < 30; i++) await Promise.resolve(); });
    expect(screen.getByText("Результат сохранения не подтверждён")).toBeTruthy();

    serverEnvelope = structuredClone(serverEnvelope);
    serverEnvelope.revision = 3;
    serverEnvelope.settings = patchBody.settings;
    serverEnvelope.device.applied_revision = 2;
    await act(async () => { await vi.advanceTimersByTimeAsync(8_000); });

    expect(screen.getByText("Сохранено в Bridge")).toBeTruthy();
    expect(screen.getByText("Ожидает применения дисплеем")).toBeTruthy();
    expect(screen.queryByText("Настройки в Bridge изменились после запроса. Ваш черновик сохранён; сравните его с актуальными данными.")).toBeNull();
    expect(screen.queryByText("Есть несохранённые изменения")).toBeNull();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
  });

  it("preserves edits made while a successful PATCH is pending", async () => {
    let resolvePatch!: (response: Response) => void;
    let submittedSettings: any;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [{ id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" }], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && init?.method === "PATCH") {
        submittedSettings = JSON.parse(String(init.body)).settings;
        return new Promise<Response>((resolve) => { resolvePatch = resolve; });
      }
      if (path.endsWith(`/devices/${firstId}/settings`)) return json(settingsEnvelope(firstId, "Hall"));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    const city = await screen.findByLabelText("Подпись города");
    fireEvent.change(city, { target: { value: "Submitted city" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить изменения" }));
    await waitFor(() => expect(resolvePatch).toBeTypeOf("function"));
    fireEvent.change(screen.getByLabelText("Подпись города"), { target: { value: "Newer city" } });
    const response = settingsEnvelope(firstId, "Hall");
    response.revision = 3;
    response.settings = submittedSettings;
    response.device.applied_revision = 2;
    resolvePatch(json(response));

    expect(await screen.findByText("Отправленные изменения сохранены. Более новые правки остались в черновике.")).toBeTruthy();
    expect((screen.getByLabelText("Подпись города") as HTMLInputElement).value).toBe("Newer city");
    expect(screen.getByText("Есть несохранённые изменения")).toBeTruthy();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
  });

  it("does not mark the newly selected display stale when an old display read fails", async () => {
    let deferOldRead = false;
    let failOldRead!: () => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/session")) return json({ authenticated: true, password_enabled: true, cloudflare_enabled: false, csrf_token: "csrf" });
      if (path.endsWith("/devices")) return json({ devices: [
        { id: firstId, name: "Hall", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.10", ssid: "Test" },
        { id: secondId, name: "Office", connected: true, last_seen: new Date().toISOString(), applied_revision: 2, desired_revision: 2, firmware: "1.0", ip: "192.0.2.11", ssid: "Test" },
      ], max_devices: 8 });
      if (path.endsWith("/connections")) return json(connections);
      if (path.endsWith("/updates")) return json({ current_version: "1.0", available_version: "", update_available: false, device_connected: true });
      if (path.endsWith(`/devices/${firstId}/settings`) && deferOldRead) {
        deferOldRead = false;
        return new Promise((_resolve, reject) => { failOldRead = () => reject(new Error("old display disconnected")); });
      }
      const match = path.match(/\/devices\/([a-f0-9]{32})\/settings$/);
      if (match) return json(settingsEnvelope(match[1], match[1] === firstId ? "Hall" : "Office"));
      return json({});
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);
    await screen.findByLabelText("Имя дисплея");
    fireEvent.click(screen.getByRole("button", { name: "Об устройстве" }));
    deferOldRead = true;
    fireEvent.click(screen.getByRole("button", { name: "Обновить статус" }));
    await waitFor(() => expect(failOldRead).toBeTypeOf("function"));

    fireEvent.change(screen.getByLabelText("Выбрать дисплей"), { target: { value: secondId } });
    expect((await screen.findAllByText("Office")).length).toBeGreaterThan(0);
    await act(async () => { failOldRead(); for (let i = 0; i < 10; i++) await Promise.resolve(); });
    expect(screen.queryByText("Не удалось обновить статус. Показано последнее известное состояние.")).toBeNull();
  });
});
