import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, normalizeWssOrigin, type AdminApi } from "./api";
import type { VoicePairingState, VoicePairingStatus } from "./types";
import { useT } from "./i18n";
import { SectionHeading } from "./ui";

type QRDetection = { rawValue: string };
type QRDetector = { detect: (source: HTMLVideoElement) => Promise<QRDetection[]> };
type QRDetectorConstructor = new (options: { formats: string[] }) => QRDetector;
type CameraWindow = Window & { BarcodeDetector?: QRDetectorConstructor };

export type { VoicePairingStatus } from "./types";

type PanelProps = {
  deviceId: string;
  status: VoicePairingStatus;
  wssOrigin: string;
  onWssOriginChange: (value: string) => void;
  notice?: { kind: "error" | "info"; text: string } | null;
  busy?: boolean;
  onSubmitPairing: (deviceId: string, payload: string, wssOrigin: string) => void;
  onDisable: (deviceId: string) => void;
  onCancel: (deviceId: string) => void;
  onRefresh?: (deviceId: string) => void;
};

const statusLabels: Record<VoicePairingStatus, string> = {
  unconfigured: "Не настроено",
  pending: "Ожидает подключения",
  applied: "Настройки применены",
  connected: "Подключено",
  revoked_cleanup_pending: "Ожидает очистки",
  error: "Ошибка настройки",
};

/** Presentation and camera input; callbacks delegate API mutations to the controller. */
export function VoicePairingPanel(props: PanelProps) {
  const t = useT();
  const previousDevice = useRef(props.deviceId);
  const videoRef = useRef<HTMLVideoElement>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const animationRef = useRef<number | null>(null);
  const scanToken = useRef(0);
  const [pairingDraft, setPairingDraft] = useState<{ deviceId: string; value: string }>({ deviceId: props.deviceId, value: "" });
  const pairingPayload = pairingDraft.deviceId === props.deviceId ? pairingDraft.value : "";
  const [pairingVisibility, setPairingVisibility] = useState<{ deviceId: string; visible: boolean }>({ deviceId: props.deviceId, visible: false });
  const showPairingPayload = pairingVisibility.deviceId === props.deviceId && pairingVisibility.visible;
  const [cameraState, setCameraState] = useState<"idle" | "starting" | "scanning" | "error" | "unsupported">("idle");

  function stopCamera() {
    scanToken.current += 1;
    if (animationRef.current !== null) cancelAnimationFrame(animationRef.current);
    animationRef.current = null;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    if (videoRef.current) videoRef.current.srcObject = null;
    setCameraState("idle");
  }

  useEffect(() => {
    if (previousDevice.current !== props.deviceId) {
      previousDevice.current = props.deviceId;
      stopCamera();
      setPairingDraft({ deviceId: props.deviceId, value: "" });
      setPairingVisibility({ deviceId: props.deviceId, visible: false });
    }
  }, [props.deviceId]);

  useEffect(() => () => {
    scanToken.current += 1;
    if (animationRef.current !== null) cancelAnimationFrame(animationRef.current);
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
  }, []);

  function clearPairingPayload() {
    setPairingDraft({ deviceId: props.deviceId, value: "" });
    setPairingVisibility({ deviceId: props.deviceId, visible: false });
  }

  function submitPairing(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const payload = pairingPayload;
    const normalizedOrigin = normalizeWssOrigin(props.wssOrigin);
    if (!payload.trim() || !normalizedOrigin || props.busy) return;
    stopCamera();
    clearPairingPayload();
    props.onWssOriginChange(normalizedOrigin);
    props.onSubmitPairing(props.deviceId, payload, normalizedOrigin);
  }

  async function startCameraScan() {
    const BarcodeDetector = (window as CameraWindow).BarcodeDetector;
    const mediaDevices = navigator.mediaDevices;
    if (!BarcodeDetector || !mediaDevices?.getUserMedia) {
      setCameraState("unsupported");
      return;
    }

    stopCamera();
    const token = ++scanToken.current;
    setCameraState("starting");
    try {
      const stream = await mediaDevices.getUserMedia({ video: { facingMode: "environment" }, audio: false });
      if (token !== scanToken.current) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      streamRef.current = stream;
      const video = videoRef.current;
      if (!video) {
        stopCamera();
        return;
      }
      video.srcObject = stream;
      await video.play();
      if (token !== scanToken.current) return;
      const detector = new BarcodeDetector({ formats: ["qr_code"] });
      setCameraState("scanning");

      const scanFrame = async () => {
        if (token !== scanToken.current || !videoRef.current) return;
        try {
          const detections = await detector.detect(videoRef.current);
          if (token !== scanToken.current) return;
          const payload = detections.find((result) => result.rawValue)?.rawValue;
          if (payload) {
            setPairingDraft({ deviceId: props.deviceId, value: payload });
            stopCamera();
            return;
          }
          animationRef.current = requestAnimationFrame(() => { void scanFrame(); });
        } catch {
          if (token === scanToken.current) {
            stopCamera();
            setCameraState("error");
          }
        }
      };
      void scanFrame();
    } catch {
      if (token === scanToken.current) {
        stopCamera();
        setCameraState("error");
      }
    }
  }

  function cancelSetup() {
    stopCamera();
    clearPairingPayload();
    props.onCancel(props.deviceId);
  }

  return (
    <section className="status-card voice-pairing" aria-labelledby="voice-pairing-title">
      <h3 id="voice-pairing-title">{t("Голосовое подключение дисплея")}</h3>
      <p>{t("Состояние голосового подключения")}: <strong>{t(statusLabels[props.status])}</strong></p>
      {props.onRefresh && <button className="button secondary" type="button" disabled={props.busy} onClick={() => props.onRefresh?.(props.deviceId)}>{t("Обновить состояние")}</button>}

      <form className="voice-pairing-form" onSubmit={submitPairing}>
        {props.notice && <p role={props.notice.kind === "error" ? "alert" : "status"}>{t(props.notice.text)}</p>}
        <div className="voice-pairing-field">
          <label>
            <span>{t("Адрес защищённого соединения Bridge")}</span>
            <input
              type="url"
              value={props.wssOrigin}
              onChange={(event) => props.onWssOriginChange(event.target.value)}
              autoComplete="url"
              aria-label={t("Адрес защищённого соединения Bridge")}
              required
            />
          </label>
          {!validWssOrigin(props.wssOrigin) && <small role="alert">{t("Введите корректный адрес WSS.")}</small>}
        </div>
        <div className="voice-pairing-field">
          <label>
            <span>{t("Код подключения")}</span>
            <input
              id="voice-pairing-code"
              type={showPairingPayload ? "text" : "password"}
              maxLength={199}
              value={pairingPayload}
              onChange={(event) => setPairingDraft({ deviceId: props.deviceId, value: event.target.value })}
              autoComplete="off"
              spellCheck={false}
              aria-label={t("Код подключения")}
            />
          </label>
          <button className="button secondary" type="button" aria-pressed={showPairingPayload} onClick={() => setPairingVisibility({ deviceId: props.deviceId, visible: !showPairingPayload })}>
            {t(showPairingPayload ? "Скрыть код" : "Показать код")}
          </button>
        </div>
        <div className="voice-pairing-help">
          <small>{t("Введите код из QR-кода или вставьте его вручную. Код действует однократно.")}</small>
          <small>{t("До 199 символов.")}</small>
          <small>{t("Код подключения не отображается и очищается при смене дисплея, отправке или отмене.")}</small>
        </div>
        <div>
          <button className="button secondary" type="button" onClick={() => void startCameraScan()} disabled={cameraState === "starting" || cameraState === "scanning"}>
            {t(cameraState === "starting" ? "Запуск камеры…" : "Сканировать QR-код камерой")}
          </button>
          {(cameraState === "starting" || cameraState === "scanning") && <button className="button secondary" type="button" onClick={stopCamera}>{t("Остановить камеру")}</button>}
          {cameraState === "unsupported" && <p role="status">{t("Сканирование камерой недоступно в этом браузере. Введите код вручную.")}</p>}
          {cameraState === "error" && <p role="alert">{t("Не удалось запустить камеру или прочитать QR-код. Введите код вручную.")}</p>}
          {(cameraState === "starting" || cameraState === "scanning") && <video ref={videoRef} muted playsInline aria-label={t("Предпросмотр камеры для QR-кода")} style={{ display: "block", width: "100%", maxHeight: 280, objectFit: "cover" }} />}
        </div>
        <div className="button-row">
          <button className="button" type="submit" disabled={!pairingPayload.trim() || !validWssOrigin(props.wssOrigin) || props.busy}>
            {t("Подключить дисплей")}
          </button>
          <button className="button secondary" type="button" onClick={cancelSetup} disabled={props.busy}>
            {t("Отменить настройку")}
          </button>
          <button className="button secondary" type="button" onClick={() => { stopCamera(); clearPairingPayload(); props.onDisable(props.deviceId); }} disabled={props.busy}>
            {t("Отключить голосовое подключение")}
          </button>
        </div>
      </form>
    </section>
  );
}

const validWssOrigin = (value: string) => normalizeWssOrigin(value) !== null;

type VoiceApi = Pick<AdminApi, "voicePairing" | "pairVoice" | "disableVoice" | "cancelVoicePairing">;
type VoiceRecord = { key: string; value: VoicePairingState };
type VoiceIssue = { key: string; kind: "unavailable" | "firmware" | "error"; message: string };

export function VoicePairingSettings({ deviceId, selectionKey, api }: { deviceId: string; selectionKey: string; api: VoiceApi }) {
  const t = useT();
  const currentKey = useRef(selectionKey);
  currentKey.current = selectionKey;
  const mounted = useRef(true);
  const operation = useRef(0);
  const [record, setRecord] = useState<VoiceRecord | null>(null);
  const [issue, setIssue] = useState<VoiceIssue | null>(null);
  const [busyKey, setBusyKey] = useState("");
  const [notice, setNotice] = useState<{ key: string; kind: "error" | "info"; text: string } | null>(null);
  const [originDraft, setOriginDraft] = useState<{ key: string; value: string } | null>(null);
  const [pollCount, setPollCount] = useState(0);
  const selected = record?.key === selectionKey ? record.value : null;
  const selectedIssue = issue?.key === selectionKey ? issue : null;
  const selectedNotice = notice?.key === selectionKey ? notice : null;
  const wssOrigin = originDraft?.key === selectionKey ? originDraft.value : selected?.wss_origin ?? "";
  const isBusy = busyKey === selectionKey;

  useLayoutEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; operation.current++; };
  }, []);

  const applyRead = useCallback(async (id: string, key: string, signal?: AbortSignal) => {
    const sequence = ++operation.current;
    try {
      const value = await api.voicePairing(id, signal);
      if (!mounted.current || currentKey.current !== key || operation.current !== sequence) return;
      setRecord({ key, value });
      setIssue(null);
      setNotice(null);
    } catch (error) {
      if (signal?.aborted || !mounted.current || currentKey.current !== key || operation.current !== sequence) return;
      setIssue({ key, kind: classifyVoiceIssue(error), message: voiceIssueMessage(error) });
    }
    return sequence;
  }, [api]);

  useEffect(() => {
    const controller = new AbortController();
    setPollCount(0);
    setIssue(null);
    setNotice(null);
    void applyRead(deviceId, selectionKey, controller.signal);
    return () => { controller.abort(); operation.current++; };
  }, [applyRead, deviceId, selectionKey]);

  useEffect(() => {
    if (!selected || isBusy || pollCount >= 5 || !["pending", "revoked_cleanup_pending"].includes(selected.status)) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void applyRead(deviceId, selectionKey, controller.signal).finally(() => {
        if (!controller.signal.aborted) setPollCount((count) => count + 1);
      });
    }, 8000);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [applyRead, deviceId, isBusy, pollCount, selected, selectionKey]);

  async function mutate(kind: "pair" | "disable" | "cancel", code = "", origin = wssOrigin) {
    const key = selectionKey;
    const id = deviceId;
    if (!selected || currentKey.current !== key || isBusy) return;
    if (kind === "disable" && selected.status === "connected" && !window.confirm(t("Отключить голосовое подключение этого дисплея?"))) return;
    const sequence = ++operation.current;
    setBusyKey(key);
    setNotice(null);
    setIssue(null);
    try {
      const value = kind === "pair" ? await api.pairVoice(id, code, origin)
        : kind === "disable" ? await api.disableVoice(id)
          : await api.cancelVoicePairing(id);
      if (mounted.current && currentKey.current === key && operation.current === sequence && value.device_id === id) {
        setRecord({ key, value });
        setOriginDraft({ key, value: value.wss_origin });
        setPollCount(0);
        setNotice({ key, kind: "info", text: "Состояние голосового подключения обновлено." });
      }
    } catch (error) {
      if (mounted.current && currentKey.current === key && operation.current === sequence) {
        setNotice({ key, kind: "error", text: voiceMutationMessage(error) });
      }
    } finally {
      if (mounted.current && currentKey.current === key) setBusyKey("");
    }
  }

  if (!selected) {
    if (selectedIssue) {
      return <section className="settings-section" aria-label={t("Голосовое подключение дисплея")}>
        <SectionHeading>{t("Голосовое подключение дисплея")}</SectionHeading>
        <p role={selectedIssue.kind === "error" ? "alert" : "status"}>{t(selectedIssue.message)}</p>
        <button className="button secondary" type="button" onClick={() => { setIssue(null); void applyRead(deviceId, selectionKey); }}>{t("Повторить")}</button>
      </section>;
    }
    return <section className="settings-section" aria-label={t("Голосовое подключение дисплея")}>
      <SectionHeading>{t("Голосовое подключение дисплея")}</SectionHeading>
      <p role="status">{t("Загружаем состояние голосового подключения…")}</p>
    </section>;
  }

  return <VoicePairingPanel
    deviceId={deviceId}
    status={selectedIssue ? "error" : selected.status}
    wssOrigin={wssOrigin}
    onWssOriginChange={(value) => setOriginDraft({ key: selectionKey, value })}
    notice={selectedIssue ? { kind: "error", text: selectedIssue.message } : selectedNotice}
    busy={isBusy}
    onSubmitPairing={(id, code, origin) => { if (id === deviceId) void mutate("pair", code, origin); }}
    onDisable={(id) => { if (id === deviceId) void mutate("disable"); }}
    onCancel={(id) => { if (id === deviceId) void mutate("cancel"); }}
    onRefresh={(id) => { if (id === deviceId) { setPollCount(0); void applyRead(id, selectionKey); } }}
  />;
}

function classifyVoiceIssue(error: unknown): VoiceIssue["kind"] {
  if (error instanceof ApiError && (error.code === "firmware_update_required" || error.status === 426)) return "firmware";
  if (error instanceof ApiError && (error.status === 404 || error.status === 501 || error.code === "unsupported_capability" || error.code === "api_not_supported")) return "unavailable";
  return "error";
}

function voiceIssueMessage(error: unknown): string {
  const kind = classifyVoiceIssue(error);
  return kind === "firmware" ? "Для голосового подключения требуется обновить прошивку дисплея."
    : kind === "unavailable" ? "API голосового подключения недоступен в этой версии Bridge."
      : "Не удалось загрузить состояние голосового подключения. Повторите запрос.";
}

function voiceMutationMessage(error: unknown): string {
  const kind = classifyVoiceIssue(error);
  return kind === "firmware" ? "Для голосового подключения требуется обновить прошивку дисплея."
    : kind === "unavailable" ? "API голосового подключения недоступен в этой версии Bridge."
      : "Не удалось выполнить действие с голосовым подключением. Проверьте состояние и повторите попытку.";
}
