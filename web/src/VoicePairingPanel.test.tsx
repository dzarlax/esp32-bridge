import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider, translationCatalogKeys } from "./i18n";
import { VoicePairingPanel, VoicePairingSettings, type VoicePairingStatus } from "./VoicePairingPanel";
import type { VoicePairingState } from "./types";
import { ApiError } from "./api";

const originalBarcodeDetector = Object.getOwnPropertyDescriptor(window, "BarcodeDetector");
const originalMediaDevices = Object.getOwnPropertyDescriptor(navigator, "mediaDevices");
const originalVideoPlay = HTMLMediaElement.prototype.play;

afterEach(() => {
  cleanup();
  if (originalBarcodeDetector) Object.defineProperty(window, "BarcodeDetector", originalBarcodeDetector);
  else Reflect.deleteProperty(window, "BarcodeDetector");
  if (originalMediaDevices) Object.defineProperty(navigator, "mediaDevices", originalMediaDevices);
  else Reflect.deleteProperty(navigator, "mediaDevices");
  HTMLMediaElement.prototype.play = originalVideoPlay;
  vi.restoreAllMocks();
});

function renderPanel(deviceId = "display-a", status: VoicePairingStatus = "unconfigured") {
  const handlers = {
    onWssOriginChange: vi.fn(),
    onSubmitPairing: vi.fn(),
    onDisable: vi.fn(),
    onCancel: vi.fn(),
  };
  const view = render(
    <LocaleProvider>
      <VoicePairingPanel
        deviceId={deviceId}
        status={status}
        wssOrigin="wss://bridge.example.test"
        {...handlers}
      />
    </LocaleProvider>,
  );
  return { ...view, handlers };
}

it("renders every safe status label without showing pairing material", () => {
  const statuses: [VoicePairingStatus, string][] = [
    ["unconfigured", "Не настроено"],
    ["pending", "Ожидает подключения"],
    ["applied", "Настройки применены"],
    ["connected", "Подключено"],
    ["revoked_cleanup_pending", "Ожидает очистки"],
    ["error", "Ошибка настройки"],
  ];
  for (const [status, label] of statuses) {
    cleanup();
    const { container } = renderPanel("display-a", status);
    fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "one-time-secret-material" } });
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getByLabelText("Код подключения").getAttribute("type")).toBe("password");
    expect(container.textContent).not.toContain("one-time-secret-material");
    expect(container.innerHTML).toContain('type="password"');
  }
});

it("sends pairing material only through submit and clears it immediately", () => {
  const { handlers } = renderPanel("display-a", "pending");
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "one-time-secret-material" } });
  fireEvent.click(screen.getByRole("button", { name: "Показать код" }));
  expect(screen.getByLabelText("Код подключения").getAttribute("type")).toBe("text");
  expect(screen.getByRole("button", { name: "Скрыть код" }).getAttribute("aria-pressed")).toBe("true");
  expect((screen.getByLabelText("Код подключения") as HTMLInputElement).maxLength).toBe(199);
  fireEvent.click(screen.getByRole("button", { name: "Подключить дисплей" }));
  expect(handlers.onSubmitPairing).toHaveBeenCalledWith("display-a", "one-time-secret-material", "wss://bridge.example.test:443");
  expect((screen.getByLabelText("Код подключения") as HTMLInputElement).value).toBe("");
  expect(screen.getByLabelText("Код подключения").getAttribute("type")).toBe("password");
});

it("clears the pairing code before disabling voice pairing", () => {
  const { handlers } = renderPanel();
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "disable-me" } });
  fireEvent.click(screen.getByRole("button", { name: "Отключить голосовое подключение" }));
  expect(handlers.onDisable).toHaveBeenCalledWith("display-a");
  expect((screen.getByLabelText("Код подключения") as HTMLInputElement).value).toBe("");
});

it("clears pairing material on cancel and when the selected device changes", async () => {
  const handlers = {
    onWssOriginChange: vi.fn(), onSubmitPairing: vi.fn(), onDisable: vi.fn(), onCancel: vi.fn(),
  };
  const view = render(<LocaleProvider><VoicePairingPanel deviceId="display-a" status="pending" wssOrigin="wss://bridge.example.test" {...handlers} /></LocaleProvider>);
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "one-time-secret-material" } });
  fireEvent.click(screen.getByRole("button", { name: "Показать код" }));
  fireEvent.click(screen.getByRole("button", { name: "Отменить настройку" }));
  expect(handlers.onCancel).toHaveBeenCalledWith("display-a");
  expect(screen.getByLabelText("Код подключения").getAttribute("type")).toBe("password");
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "stale-payload" } });
  view.rerender(<LocaleProvider><VoicePairingPanel deviceId="display-b" status="unconfigured" wssOrigin="wss://bridge.example.test" {...handlers} /></LocaleProvider>);
  await waitFor(() => expect((screen.getByLabelText("Код подключения") as HTMLInputElement).value).toBe(""));
  expect((screen.getByLabelText("Код подключения") as HTMLInputElement).value).toBe("");
});

it("requests camera permission only after a click and leaves manual entry available when native QR support is absent", () => {
  const getUserMedia = vi.fn();
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
  renderPanel();
  expect(getUserMedia).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  expect(getUserMedia).not.toHaveBeenCalled();
  expect(screen.getByRole("status").textContent).toContain("Сканирование камерой недоступно");
  expect(screen.getByLabelText("Код подключения")).toBeTruthy();
});

it("scans a QR code without echoing it and stops the camera tracks", async () => {
  const stop = vi.fn();
  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream;
  const getUserMedia = vi.fn().mockResolvedValue(stream);
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
  Object.defineProperty(window, "BarcodeDetector", {
    configurable: true,
    value: class { detect = vi.fn().mockResolvedValue([{ rawValue: "qr-one-time-capability" }]); constructor(_options: { formats: string[] }) {} },
  });
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  const { handlers } = renderPanel();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  expect(getUserMedia).toHaveBeenCalledWith({ video: { facingMode: "environment" }, audio: false });
  await waitFor(() => expect(stop).toHaveBeenCalled());
  const code = screen.getByLabelText("Код подключения") as HTMLInputElement;
  expect(code.type).toBe("password");
  expect(code.value).toBe("qr-one-time-capability");
  expect(screen.getByRole("region").textContent).not.toContain("qr-one-time-capability");
  expect(handlers.onSubmitPairing).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Подключить дисплей" }));
  expect(handlers.onSubmitPairing).toHaveBeenCalledWith("display-a", "qr-one-time-capability", "wss://bridge.example.test:443");
});

it("stops camera tracks on cancel, unmount, and selected-device switch", async () => {
  const stop = vi.fn();
  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream;
  const getUserMedia = vi.fn().mockResolvedValue(stream);
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
  Object.defineProperty(window, "BarcodeDetector", {
    configurable: true,
    value: class { detect = vi.fn().mockResolvedValue([]); constructor(_options: { formats: string[] }) {} },
  });
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  const handlers = { onWssOriginChange: vi.fn(), onSubmitPairing: vi.fn(), onDisable: vi.fn(), onCancel: vi.fn() };
  const view = render(<LocaleProvider><VoicePairingPanel deviceId="display-a" status="pending" wssOrigin="wss://bridge.example.test" {...handlers} /></LocaleProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Остановить камеру" })).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "Отменить настройку" }));
  expect(stop).toHaveBeenCalled();

  stop.mockClear();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Остановить камеру" })).toBeTruthy());
  view.rerender(<LocaleProvider><VoicePairingPanel deviceId="display-b" status="pending" wssOrigin="wss://bridge.example.test" {...handlers} /></LocaleProvider>);
  await waitFor(() => expect(stop).toHaveBeenCalled());

  stop.mockClear();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Остановить камеру" })).toBeTruthy());
  view.unmount();
  expect(stop).toHaveBeenCalled();
});

it("stops a stream returned after the user cancels while permission is pending", async () => {
  let resolveStream!: (stream: MediaStream) => void;
  const getUserMedia = vi.fn(() => new Promise<MediaStream>((resolve) => { resolveStream = resolve; }));
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
  Object.defineProperty(window, "BarcodeDetector", {
    configurable: true,
    value: class { detect = vi.fn().mockResolvedValue([]); constructor(_options: { formats: string[] }) {} },
  });
  const stop = vi.fn();
  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream;
  HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
  renderPanel();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  expect(getUserMedia).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Отменить настройку" }));
  resolveStream(stream);
  await waitFor(() => expect(stop).toHaveBeenCalled());
  expect(screen.getByLabelText("Код подключения")).toHaveProperty("value", "");
});

it("does not restart scanning when video playback resolves after cancellation", async () => {
  const stop = vi.fn();
  const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream;
  const getUserMedia = vi.fn().mockResolvedValue(stream);
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
  Object.defineProperty(window, "BarcodeDetector", {
    configurable: true,
    value: class { detect = vi.fn().mockResolvedValue([]); constructor(_options: { formats: string[] }) {} },
  });
  let resolvePlay!: () => void;
  HTMLMediaElement.prototype.play = vi.fn(() => new Promise<void>((resolve) => { resolvePlay = resolve; }));
  const { handlers } = renderPanel();
  fireEvent.click(screen.getByRole("button", { name: "Сканировать QR-код камерой" }));
  await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled());
  fireEvent.click(screen.getByRole("button", { name: "Отменить настройку" }));
  resolvePlay();
  await waitFor(() => expect(stop).toHaveBeenCalled());
  expect(screen.queryByRole("button", { name: "Остановить камеру" })).toBeNull();
  expect(screen.queryByLabelText("Предпросмотр камеры для QR-кода")).toBeNull();
  expect(handlers.onSubmitPairing).not.toHaveBeenCalled();
});

it("does not submit a previous device's code immediately after the selected device changes", () => {
  const handlers = { onWssOriginChange: vi.fn(), onSubmitPairing: vi.fn(), onDisable: vi.fn(), onCancel: vi.fn() };
  const view = render(<LocaleProvider><VoicePairingPanel deviceId="display-a" status="pending" wssOrigin="" {...handlers} /></LocaleProvider>);
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "device-a-only" } });
  fireEvent.click(screen.getByRole("button", { name: "Показать код" }));
  view.rerender(<LocaleProvider><VoicePairingPanel deviceId="display-b" status="pending" wssOrigin="" {...handlers} /></LocaleProvider>);
  expect(screen.getByLabelText("Код подключения").getAttribute("type")).toBe("password");
  fireEvent.click(screen.getByRole("button", { name: "Подключить дисплей" }));
  expect(handlers.onSubmitPairing).not.toHaveBeenCalled();
  expect((screen.getByLabelText("Код подключения") as HTMLInputElement).value).toBe("");
});

function voiceState(deviceId: string, status: VoicePairingStatus = "unconfigured"): VoicePairingState {
  return { device_id: deviceId, status, wss_origin: "wss://bridge.example.test:443" };
}

function voiceApi(overrides: Partial<{
  voicePairing: (id: string, signal?: AbortSignal) => Promise<VoicePairingState>;
  pairVoice: (id: string, code: string, origin: string) => Promise<VoicePairingState>;
  disableVoice: (id: string) => Promise<VoicePairingState>;
  cancelVoicePairing: (id: string) => Promise<VoicePairingState>;
}> = {}) {
  return {
    voicePairing: vi.fn((id: string) => Promise.resolve(voiceState(id))),
    pairVoice: vi.fn((id: string) => Promise.resolve(voiceState(id, "pending"))),
    disableVoice: vi.fn((id: string) => Promise.resolve(voiceState(id, "unconfigured"))),
    cancelVoicePairing: vi.fn((id: string) => Promise.resolve(voiceState(id))),
    ...overrides,
  };
}

it("loads and pairs only the selected device, then never renders the pairing code", async () => {
  const api = voiceApi();
  render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByLabelText("Код подключения")).toBeTruthy());
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "one-time-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Подключить дисплей" }));
  await waitFor(() => expect(api.pairVoice).toHaveBeenCalledWith("display-a", "one-time-secret", "wss://bridge.example.test:443"));
  expect(screen.getByRole("region").textContent).not.toContain("one-time-secret");
  expect(screen.getByLabelText("Код подключения")).toHaveProperty("value", "");
});

it("ignores a late selected-device read and late pairing rejection after switching devices", async () => {
  let resolveA!: (state: VoicePairingState) => void;
  let rejectPairA!: (error: Error) => void;
  const api = voiceApi({
    voicePairing: vi.fn((id: string) => id === "display-a"
      ? new Promise<VoicePairingState>((resolve) => { resolveA = resolve; })
      : Promise.resolve(voiceState(id))),
    pairVoice: vi.fn(() => new Promise<VoicePairingState>((_resolve, reject) => { rejectPairA = reject; })),
  });
  const view = render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  view.rerender(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-b" selectionKey="display-b:2" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByLabelText("Код подключения")).toBeTruthy());
  resolveA(voiceState("display-a", "connected"));
  fireEvent.change(screen.getByLabelText("Код подключения"), { target: { value: "old-device-code" } });
  fireEvent.click(screen.getByRole("button", { name: "Подключить дисплей" }));
  await waitFor(() => expect(api.pairVoice).toHaveBeenCalledWith("display-b", "old-device-code", "wss://bridge.example.test:443"));
  view.rerender(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-c" selectionKey="display-c:3" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByLabelText("Код подключения")).toBeTruthy());
  rejectPairA(new Error("late failure with sensitive details"));
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(screen.getByRole("region").textContent).not.toContain("late failure");
  expect(screen.getByRole("region").textContent).not.toContain("old-device-code");
});

it("shows unsupported API as unavailable instead of a ready state", async () => {
  const api = voiceApi({ voicePairing: vi.fn().mockRejectedValue(new ApiError("not_found", 404)) });
  render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByRole("status").textContent).toContain("API голосового подключения недоступен"));
  expect(screen.queryByText("Подключено")).toBeNull();
  expect(screen.queryByLabelText("Код подключения")).toBeNull();
});

it("refreshes the selected device status on demand", async () => {
  const voicePairing = vi.fn()
    .mockResolvedValueOnce(voiceState("display-a", "pending"))
    .mockResolvedValueOnce(voiceState("display-a", "connected"));
  const api = voiceApi({ voicePairing });
  render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByText("Ожидает подключения")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "Обновить состояние" }));
  await waitFor(() => expect(screen.getByText("Подключено")).toBeTruthy());
  expect(api.voicePairing).toHaveBeenCalledTimes(2);
});

it("drops a connected claim when refresh fails and recovers on retry", async () => {
  const voicePairing = vi.fn()
    .mockResolvedValueOnce(voiceState("display-a", "connected"))
    .mockRejectedValueOnce(new Error("private transport detail"))
    .mockResolvedValueOnce(voiceState("display-a", "connected"));
  const api = voiceApi({ voicePairing, cancelVoicePairing: vi.fn().mockResolvedValue(voiceState("display-a", "connected")) });
  render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByText("Подключено")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "Отменить настройку" }));
  await waitFor(() => expect(screen.getByRole("status").textContent).toContain("Состояние голосового подключения обновлено"));
  fireEvent.click(screen.getByRole("button", { name: "Обновить состояние" }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Не удалось загрузить состояние"));
  expect(screen.queryByText("Подключено")).toBeNull();
  expect(screen.getByRole("region").textContent).not.toContain("private transport detail");
  fireEvent.click(screen.getByRole("button", { name: "Обновить состояние" }));
  await waitFor(() => expect(screen.getByText("Подключено")).toBeTruthy());
  expect(screen.queryByRole("alert")).toBeNull();
});

it("asks before disabling a connected voice pairing", async () => {
  const api = voiceApi({ voicePairing: vi.fn((id: string) => Promise.resolve(voiceState(id, "connected"))) });
  vi.stubGlobal("confirm", vi.fn(() => false));
  render(<LocaleProvider><VoicePairingSettings api={api} deviceId="display-a" selectionKey="display-a:1" /></LocaleProvider>);
  await waitFor(() => expect(screen.getByRole("button", { name: "Отключить голосовое подключение" })).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: "Отключить голосовое подключение" }));
  expect(window.confirm).toHaveBeenCalled();
  expect(api.disableVoice).not.toHaveBeenCalled();
});

it("exposes accessible inputs and keeps all translated messages in each catalog", () => {
  renderPanel();
  expect(screen.getByRole("region", { name: "Голосовое подключение дисплея" })).toBeTruthy();
  expect(screen.getByLabelText("Адрес защищённого соединения Bridge")).toBeTruthy();
  expect(screen.getByLabelText("Код подключения")).toBeTruthy();
  const addedMessages = [
    "Голосовое подключение дисплея", "Состояние голосового подключения", "Не настроено",
    "Ожидает подключения", "Настройки применены", "Подключено", "Ожидает очистки",
    "Ошибка настройки", "Адрес защищённого соединения Bridge", "Введите корректный адрес WSS.",
    "Код подключения", "Введите код из QR-кода или вставьте его вручную. Код действует однократно.",
    "Сканировать QR-код камерой", "Запуск камеры…", "Остановить камеру", "Предпросмотр камеры для QR-кода",
    "Сканирование камерой недоступно в этом браузере. Введите код вручную.",
    "Не удалось запустить камеру или прочитать QR-код. Введите код вручную.",
    "Подключить дисплей", "Отключить голосовое подключение", "Отменить настройку",
    "Показать код", "Скрыть код", "До 199 символов.",
    "Код подключения очищен.", "Код подключения не отображается и очищается при смене дисплея, отправке или отмене.",
    "Загружаем состояние голосового подключения…", "API голосового подключения недоступен в этой версии Bridge.",
    "Для голосового подключения требуется обновить прошивку дисплея.", "Не удалось загрузить состояние голосового подключения. Повторите запрос.",
    "Состояние голосового подключения обновлено.", "Не удалось выполнить действие с голосовым подключением. Проверьте состояние и повторите попытку.",
    "Отключить голосовое подключение этого дисплея?",
  ];
  for (const locale of ["en", "sr-Latn"] as const) {
    const translated = new Set(translationCatalogKeys(locale));
    for (const key of addedMessages) expect(translated.has(key as never), `${locale}: ${key}`).toBe(true);
  }
});
