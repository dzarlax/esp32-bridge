import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AdminApi, ApiError, isRevisionConflict } from "./api";
import { photoTimingError, type PhotoTiming } from "./validation";
import { ConnectionsPage } from "./connections-page";
import { ConfirmDialog, DevicePage, UpdatesPage } from "./device-pages";
import { NetworkPage, ServicesPage } from "./service-pages";
import { PhotosPage, ScreenPage } from "./screen-photo-pages";
import type { PageProps } from "./page-types";
import {
  clone,
  isCommandFinal,
  SECTION_LIST,
  type Command,
  type ConnectionsEnvelope,
  type DeviceList,
  type Envelope,
  type SectionId,
  type SecretKey,
  type SharedConnections,
  type Session,
  type Settings,
  type UpdateInfo,
} from "./types";
import { canRun, Field, Icon, type Ask, StatusPill, TextInput } from "./ui";
import { LanguageSelect, LocaleProvider, useT } from "./i18n";
import { hasCurrentApplicationAck, sameSettings } from "./settings-apply-status";
import { SettingsApplyStatusCard } from "./settings-apply-status-card";

const api = new AdminApi();
function Login({ session, onLogin }: { session: Session; onLogin: (session: Session) => void }) {
  const t = useT();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      api.setCsrf(session.csrf_token);
      const result = await api.login(password);
      setPassword("");
      onLogin(result);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось войти");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <LanguageSelect />
        <div className="brand-mark"><Icon name="monitor" size={30} /></div>
        <h1>HomeDash</h1>
        <p>{t("Войдите, чтобы управлять настройками дисплеев.")}</p>
        {session.password_enabled && <Field label={t("Пароль администратора")}><TextInput autoFocus type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required /></Field>}
        {error && <div className="alert error" role="alert">{t(error)}</div>}
        {session.password_enabled && <button className="button primary full" disabled={busy}>{busy ? t("Вход…") : t("Войти")}</button>}
        {session.cloudflare_enabled && <a className="button secondary full" href="/api/admin/v1/auth/cloudflare"><Icon name="lock" size={17} /> {" "}{t("Войти через Cloudflare Access")}</a>}
        {!session.password_enabled && !session.cloudflare_enabled && <div className="alert error">{t("Для панели не настроен способ входа.")}</div>}
      </form>
    </main>
  );
}

export default function App() {
  return <LocaleProvider><AppContent /></LocaleProvider>;
}

function AppContent() {
  const t = useT();
  const [session, setSession] = useState<Session | null>(null);
  const [sessionError, setSessionError] = useState("");
  const [devices, setDevices] = useState<DeviceList | null>(null);
  const [selectedDeviceId, setSelectedDeviceId] = useState("");
  const selectedDeviceRef = useRef("");
  const selectionEpoch = useRef(0);
  const requestSequence = useRef(0);
  const [envelope, setEnvelope] = useState<Envelope | null>(null);
  const [draft, setDraft] = useState<Settings | null>(null);
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(false);
  const baseRevisionRef = useRef<number | null>(null);
  const [networkCredentials, setNetworkCredentials] = useState<Record<string, string>>({});
  const [connections, setConnections] = useState<ConnectionsEnvelope | null>(null);
  const [section, setSection] = useState<SectionId>("screen");
  const settingsFieldsRef = useRef<HTMLDivElement>(null);
  const [invalidTiming, setInvalidTiming] = useState<PhotoTiming | null>(null);
  useEffect(() => {
    if (!invalidTiming) return;
    settingsFieldsRef.current?.querySelector<HTMLInputElement>(`[data-photo-timing="${invalidTiming}"]`)?.focus();
    setInvalidTiming(null);
  }, [invalidTiming, section]);
  const [showConnections, setShowConnections] = useState(false);
  const connectionsDirtyRef = useRef(false);
  const [mobileNav, setMobileNav] = useState(false);
  const [alert, setAlert] = useState<{ kind: "success" | "error" | "info"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<Ask | null>(null);
  const [command, setCommand] = useState<Command | null>(null);
  const [updates, setUpdates] = useState<UpdateInfo | null>(null);
  const [networkEditing, setNetworkEditing] = useState(false);
  const [serviceDetail, setServiceDetail] = useState<keyof Settings["services"]>("weather");
  const [conflict, setConflict] = useState(false);
  const formEditSequence = useRef(0);
  const draftRef = useRef<Settings | null>(null);
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "uncertain">("idle");
  const [statusStale, setStatusStale] = useState(false);
  const [checkingStatus, setCheckingStatus] = useState(false);
  const pendingSinceRef = useRef(new Map<string, number>());
  const saveStateRevisionRef = useRef<number | null>(null);
  const ambiguousSaveRef = useRef<{
    deviceId: string;
    epoch: number;
    baseRevision: number;
    baseSettings: Settings;
    sentSettings: Settings;
    sentCredentials: boolean;
  } | null>(null);

  function observeApplication(envelope: Envelope) {
    const key = `${envelope.device_id}:${envelope.revision}`;
    for (const existing of pendingSinceRef.current.keys()) {
      if (existing.startsWith(`${envelope.device_id}:`) && existing !== key) pendingSinceRef.current.delete(existing);
    }
    if (hasCurrentApplicationAck(envelope)) pendingSinceRef.current.delete(key);
    else if (!pendingSinceRef.current.has(key)) pendingSinceRef.current.set(key, Date.now());
  }

  const selectedProfile = useMemo(() => devices?.devices.find((device) => device.id === selectedDeviceId) ?? null, [devices, selectedDeviceId]);
  const current = SECTION_LIST.find((item) => item.id === section)!;

  const loadDevice = useCallback(async (deviceId: string, preserveDraft = false) => {
    if (!deviceId || selectedDeviceRef.current !== deviceId) return null;
    const epoch = selectionEpoch.current;
    const sequence = ++requestSequence.current;
    const next = await api.settings(deviceId);
    if (sequence !== requestSequence.current || selectionEpoch.current !== epoch || selectedDeviceRef.current !== deviceId || next.device_id !== deviceId) return null;
    setEnvelope(next);
    setStatusStale(false);
    observeApplication(next);
    if (!ambiguousSaveRef.current && saveStateRevisionRef.current !== null && next.revision !== saveStateRevisionRef.current) {
      saveStateRevisionRef.current = null;
      ambiguousSaveRef.current = null;
      setSaveState("idle");
    }
    setCommand(next.device.command);
    if (ambiguousSaveRef.current?.deviceId === deviceId && ambiguousSaveRef.current.epoch === epoch) {
      await reconcileAmbiguousSave(next, deviceId, epoch);
    }
    if (!preserveDraft || !dirtyRef.current) {
      const nextDraft = clone(next.settings);
      draftRef.current = nextDraft;
      setDraft(nextDraft);
      baseRevisionRef.current = next.revision;
      setConflict(false);
    } else if (baseRevisionRef.current !== null && next.revision !== baseRevisionRef.current) {
      setConflict(true);
    }
    return next;
  }, []);

  const loadConnections = useCallback(async () => {
    const next = await api.connections();
    setConnections(next);
    return next;
  }, []);

  async function refreshDevicesAndSelect() {
    const result = await api.devices();
    const nextDevices = Array.isArray(result.devices) ? result : { ...result, devices: [] };
    setDevices(nextDevices);
    const queryId = new URLSearchParams(window.location.search).get("device_id");
    const existingId = nextDevices.devices.some((item) => item.id === selectedDeviceRef.current)
      ? selectedDeviceRef.current
      : nextDevices.devices.some((item) => item.id === queryId)
        ? queryId!
        : nextDevices.devices[0]?.id ?? "";
    if (existingId !== selectedDeviceRef.current) {
      selectedDeviceRef.current = existingId;
      selectionEpoch.current++;
      setSelectedDeviceId(existingId);
      updateDeviceQuery(existingId);
      setEnvelope(null);
      setDraft(null);
      draftRef.current = null;
      setDirty(false);
      dirtyRef.current = false;
      baseRevisionRef.current = null;
      setSaveState("idle");
      saveStateRevisionRef.current = null;
      ambiguousSaveRef.current = null;
      pendingSinceRef.current.clear();
      setStatusStale(false);
      if (existingId) await loadDevice(existingId);
      else setEnvelope(null);
      return;
    }
  }

  useEffect(() => {
    api.session().then((value) => {
      api.setCsrf(value.csrf_token);
      setSession(value);
    }).catch((reason) => setSessionError(reason instanceof Error ? reason.message : "Не удалось проверить сеанс"));
  }, []);

  useEffect(() => {
    if (!session?.authenticated) return;
    void refreshDevicesAndSelect().catch((reason) => setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Не удалось загрузить дисплеи" }));
    void loadConnections().catch(() => undefined);
  }, [session?.authenticated]);

  useEffect(() => {
    if (!session?.authenticated) return;
    const timer = window.setInterval(() => {
      void refreshDevicesAndSelect().catch(() => undefined);
      const currentId = selectedDeviceRef.current;
      if (currentId) {
        const currentEpoch = selectionEpoch.current;
        void loadDevice(currentId, true).catch(() => {
          if (selectedDeviceRef.current === currentId && selectionEpoch.current === currentEpoch) setStatusStale(true);
        });
        void api.updates(currentId).then((result) => {
          if (selectedDeviceRef.current === currentId) setUpdates(result);
        }).catch(() => undefined);
      }
      if (showConnections) void loadConnections().catch(() => undefined);
    }, 8000);
    return () => window.clearInterval(timer);
  }, [session?.authenticated, showConnections, loadConnections, loadDevice]);

  useEffect(() => {
    const currentId = selectedDeviceId;
    if (!session?.authenticated || !currentId) return;
    void api.updates(currentId).then((result) => {
      if (selectedDeviceRef.current === currentId) setUpdates(result);
    }).catch(() => undefined);
  }, [session?.authenticated, selectedDeviceId]);

  useEffect(() => {
    const handler = () => {
      setSession((previous) => previous ? { ...previous, authenticated: false } : previous);
      setDevices(null);
      selectedDeviceRef.current = "";
      selectionEpoch.current++;
      setSelectedDeviceId("");
      setEnvelope(null);
      setDraft(null);
      draftRef.current = null;
      dirtyRef.current = false;
      setDirty(false);
      setSaveState("idle");
      saveStateRevisionRef.current = null;
      ambiguousSaveRef.current = null;
      pendingSinceRef.current.clear();
      setStatusStale(false);
      setNetworkCredentials({});
      setConnections(null);
      setAlert({ kind: "info", text: "Сеанс завершён. Войдите снова." });
    };
    window.addEventListener("admin-auth-expired", handler);
    return () => window.removeEventListener("admin-auth-expired", handler);
  }, []);

  useEffect(() => {
    if (!session?.authenticated) return;
    function onEscape(event: KeyboardEvent) {
      if (event.key === "Escape") { setConfirm(null); setMobileNav(false); }
    }
    window.addEventListener("keydown", onEscape);
    return () => window.removeEventListener("keydown", onEscape);
  }, [session?.authenticated]);

  function retrySession() {
    setSessionError("");
    api.session().then((value) => { api.setCsrf(value.csrf_token); setSession(value); })
      .catch((reason) => setSessionError(reason instanceof Error ? reason.message : "Не удалось проверить сеанс"));
  }

  function updateDeviceQuery(deviceId: string) {
    const url = new URL(window.location.href);
    if (deviceId) url.searchParams.set("device_id", deviceId);
    else url.searchParams.delete("device_id");
    window.history.replaceState({}, "", `${url.pathname}${url.search}${url.hash}`);
  }

  function clearProfileDraft() {
    const nextDraft = envelope ? clone(envelope.settings) : null;
    draftRef.current = nextDraft;
    setDraft(nextDraft);
    baseRevisionRef.current = envelope?.revision ?? null;
    dirtyRef.current = false;
    setDirty(false);
    setNetworkCredentials({});
    setNetworkEditing(false);
    setConflict(false);
  }

  function cancel() {
    clearProfileDraft();
    setAlert(null);
  }

  function switchDevice(deviceId: string) {
    if (deviceId === selectedDeviceRef.current || busy) return;
    if (dirtyRef.current && !window.confirm(t("Есть несохранённые изменения. Переключить дисплей и отменить их?"))) return;
    clearProfileDraft();
    requestSequence.current++;
    selectionEpoch.current++;
    selectedDeviceRef.current = deviceId;
    setSelectedDeviceId(deviceId);
    updateDeviceQuery(deviceId);
    setEnvelope(null);
    setUpdates(null);
    setCommand(null);
    setSaveState("idle");
    saveStateRevisionRef.current = null;
    ambiguousSaveRef.current = null;
    pendingSinceRef.current.clear();
    setStatusStale(false);
    setAlert(null);
    void loadDevice(deviceId).catch((reason) => setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Не удалось загрузить настройки дисплея" }));
  }

  function edit(next: Settings) {
    formEditSequence.current++;
    draftRef.current = next;
    setDraft(next);
    dirtyRef.current = true;
    setDirty(true);
    setAlert(null);
  }

  async function save(): Promise<string | null> {
    const deviceId = selectedDeviceRef.current;
    if (!draft || !envelope || !deviceId || busy) return "Сохранение сейчас недоступно. Повторите попытку.";
    const validation = photoTimingError(draft.photos);
    if (validation) {
      setAlert({ kind: "error", text: validation.message });
      if (section !== "screen" && section !== "photos") setSection("photos");
      if (validation.field === "interval_seconds" && section === "screen") setSection("photos");
      setInvalidTiming(validation.field);
      return validation.message;
    }
    const epoch = selectionEpoch.current;
    const sentSettings = clone(draft);
    const submittedEditSequence = formEditSequence.current;
    const baseSettings = clone(envelope.settings);
    const baseRevision = baseRevisionRef.current ?? envelope.revision;
    const sentCredentials = Object.keys(networkCredentials).length > 0;
    setBusy(true);
    setSaveState("saving");
    setAlert(null);
    try {
      const result = await api.save(deviceId, sentSettings, baseRevision, networkCredentials);
      if (selectedDeviceRef.current !== deviceId || selectionEpoch.current !== epoch || result.device_id !== deviceId) return "Выбранный дисплей изменился; результат сохранения не применён к текущему экрану.";
      requestSequence.current++;
      setEnvelope(result);
      observeApplication(result);
      const editedWhileSaving = formEditSequence.current !== submittedEditSequence;
      if (!editedWhileSaving) {
        const savedDraft = clone(result.settings);
        draftRef.current = savedDraft;
        setDraft(savedDraft);
      }
      baseRevisionRef.current = result.revision;
      saveStateRevisionRef.current = result.revision;
      ambiguousSaveRef.current = null;
      setSaveState("saved");
      setStatusStale(false);
      dirtyRef.current = editedWhileSaving;
      setDirty(editedWhileSaving);
      if (!editedWhileSaving) {
        setNetworkCredentials({});
        setNetworkEditing(false);
      }
      setConflict(false);
      setAlert({ kind: "success", text: editedWhileSaving
        ? "Отправленные изменения сохранены. Более новые правки остались в черновике."
        : "Изменения сохранены. Дисплей применит их при следующей синхронизации." });
      return null;
    } catch (reason) {
      if (selectedDeviceRef.current !== deviceId || selectionEpoch.current !== epoch) return "Выбранный дисплей изменился; результат запроса проигнорирован.";
      const transportUnknown = (reason instanceof ApiError && (reason.status === 0 || reason.status === 502)) || (reason instanceof Error && reason.name === "AbortError");
      if (transportUnknown) {
        ambiguousSaveRef.current = { deviceId, epoch, baseRevision, baseSettings, sentSettings, sentCredentials };
        setSaveState("uncertain");
        const message = "Ответ на сохранение не получен. Черновик сохранён; проверьте статус перед повторной попыткой.";
        setAlert({ kind: "info", text: message });
        await checkApplicationStatus();
        return message;
      }
      if (isRevisionConflict(reason)) {
        const conflictReadSequence = ++requestSequence.current;
        const latest = await api.settings(deviceId).catch(() => null);
        if (latest && conflictReadSequence === requestSequence.current && selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch) {
          setEnvelope(latest);
          observeApplication(latest);
          setCommand(latest.device.command);
          baseRevisionRef.current = latest.revision;
          setDraft((current) => {
            const merged = current ? mergeSharedProjection(current, latest.settings) : current;
            draftRef.current = merged;
            return merged;
          });
        }
        setConflict(true);
        setSaveState("idle");
        const message = latest && conflictReadSequence === requestSequence.current ? "Настройки изменились на сервере. Ваш черновик сохранён — сравните его с актуальными данными и повторите изменения." : "Настройки изменились на сервере. Не удалось загрузить актуальную версию.";
        setAlert({ kind: "error", text: message });
        return message;
      }
      const message = reason instanceof Error ? reason.message : "Сохранение не выполнено. Прежние настройки сохранены.";
      setSaveState("idle");
      setAlert({ kind: "error", text: message });
      return message;
    } finally { setBusy(false); }
  }

  async function reconcileAmbiguousSave(latest: Envelope, deviceId: string, epoch: number) {
    if (selectedDeviceRef.current !== deviceId || selectionEpoch.current !== epoch || latest.device_id !== deviceId) return;
    const attempt = ambiguousSaveRef.current;
    if (!attempt || attempt.deviceId !== deviceId || attempt.epoch !== epoch) return;
    setEnvelope(latest);
    observeApplication(latest);
    setStatusStale(false);
    setCommand(latest.device.command);
    if (latest.revision > attempt.baseRevision && sameSettings(latest.settings, attempt.sentSettings)) {
      baseRevisionRef.current = latest.revision;
      setConflict(false);
      const currentDraft = draftRef.current;
      const draftMatchesSent = currentDraft !== null && sameSettings(currentDraft, attempt.sentSettings);
      if (draftMatchesSent && !attempt.sentCredentials) {
        const confirmedDraft = clone(latest.settings);
        draftRef.current = confirmedDraft;
        setDraft(confirmedDraft);
        dirtyRef.current = false;
        setDirty(false);
      }
      saveStateRevisionRef.current = latest.revision;
      setSaveState(attempt.sentCredentials ? "uncertain" : "saved");
      if (!attempt.sentCredentials) ambiguousSaveRef.current = null;
      setAlert({ kind: "info", text: attempt.sentCredentials
        ? "Настройки найдены в Bridge. Записанный ключ невозможно подтвердить через настройки."
        : "Настройки найдены в Bridge. Дисплей подтвердит применение отдельно." });
      return;
    }
    if (latest.revision === attempt.baseRevision && sameSettings(latest.settings, attempt.baseSettings)) {
      // The PATCH may still be in flight and commit after this read. Keep the outcome uncertain.
      return;
    }
    ambiguousSaveRef.current = null;
    saveStateRevisionRef.current = null;
    setConflict(true);
    setSaveState("idle");
    setAlert({ kind: "error", text: "Настройки в Bridge изменились после запроса. Ваш черновик сохранён; сравните его с актуальными данными." });
  }

  async function checkApplicationStatus() {
    const deviceId = selectedDeviceRef.current;
    const epoch = selectionEpoch.current;
    if (!deviceId || checkingStatus) return;
    setCheckingStatus(true);
    try {
      await loadDevice(deviceId, true);
    } catch {
      if (selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch) setStatusStale(true);
    } finally {
      setCheckingStatus(false);
    }
  }

  async function refreshSelectedStatus() {
    const deviceId = selectedDeviceRef.current;
    const epoch = selectionEpoch.current;
    if (!deviceId) return;
    try {
      await loadDevice(deviceId, true);
      if (selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch)
        setAlert({ kind: "info", text: "Статус обновлён из последнего ответа устройства." });
    } catch (reason) {
      if (selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch) {
        setStatusStale(true);
        setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Не удалось обновить статус" });
      }
    }
  }

  async function doAction(action: Ask["action"]) {
    const deviceId = selectedDeviceRef.current;
    if (!envelope || !deviceId || busy) return;
    if (!canRun(envelope.device, action)) { setAlert({ kind: "error", text: "Устройство не в сети или не сообщило поддержку этой команды." }); return; }
    const epoch = selectionEpoch.current;
    setBusy(true);
    setAlert(null);
    try {
      const result = await api.action(deviceId, action);
      if (selectedDeviceRef.current !== deviceId || selectionEpoch.current !== epoch) return;
      setCommand(result.command);
      setConfirm(null);
      setAlert({ kind: "info", text: "Команда принята. Результат появится после подтверждения устройством." });
      const latest = await api.settings(deviceId).catch(() => null);
      if (latest && selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch) {
        setEnvelope(latest);
        setCommand(latest.device.command ?? result.command);
      }
    } catch (reason) {
      if (selectedDeviceRef.current === deviceId && selectionEpoch.current === epoch) setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Команда не выполнена" });
    } finally { setBusy(false); }
  }

  async function logout() {
    try { await api.logout(); } catch { /* the local session still clears if the server session expired */ }
    setSession((previous) => previous ? { ...previous, authenticated: false } : previous);
    setDevices(null); setEnvelope(null); setDraft(null); setConnections(null);
    selectedDeviceRef.current = ""; selectionEpoch.current++; setSelectedDeviceId("");
    dirtyRef.current = false; setDirty(false); setNetworkCredentials({});
  }

  function navigate(to: SectionId) {
    if (dirtyRef.current && !window.confirm(t("Есть несохранённые настройки дисплея. Перейти и отменить их?"))) return;
    if (connectionsDirtyRef.current && !window.confirm(t("Есть несохранённые общие подключения. Закрыть форму и отменить изменения?"))) return;
    if (dirtyRef.current) cancel();
    if (connectionsDirtyRef.current) connectionsDirtyRef.current = false;
    setSection(to); setShowConnections(false); setMobileNav(false); setAlert(null);
  }

  function openConnections() {
    if (dirtyRef.current && !window.confirm(t("Есть несохранённые настройки дисплея. Открыть общие подключения и оставить черновик в текущем дисплее?"))) return;
    setShowConnections(true); setMobileNav(false); setAlert(null);
    void loadConnections().catch((reason) => setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Не удалось загрузить общие подключения" }));
  }

  async function saveConnections(value: SharedConnections, revision: number, secrets: Partial<Record<SecretKey, string | null>>, confirmDisconnect: boolean) {
    try {
      const next = await api.saveConnections(value, revision, secrets, confirmDisconnect);
      setConnections(next);
      if (selectedDeviceRef.current) {
        const currentId = selectedDeviceRef.current;
        await loadDevice(currentId, true).catch(() => null);
      }
      return next;
    } catch (reason) {
      if (isRevisionConflict(reason)) {
        void loadConnections().catch(() => undefined);
        throw new Error("Общие подключения изменились в другом сеансе. Форма сохранена; загрузите актуальные данные перед повторной попыткой.");
      }
      throw reason;
    }
  }

  if (!session) return sessionError ? <main className="login-wrap"><section className="login-card"><div className="brand-mark"><Icon name="monitor" size={30} /></div><h1>HomeDash</h1><LanguageSelect /><div className="alert error" role="alert">{t(sessionError)}</div><button className="button primary full" onClick={retrySession}>{t("Повторить проверку сеанса")}</button></section></main> : <div className="loading-screen" role="status">{t("Проверяем сеанс…")}</div>;
  if (!session.authenticated) return <Login session={session} onLogin={(value) => { api.setCsrf(value.csrf_token); setSession(value); setAlert(null); }} />;

  const dirtySecrets = Object.keys(networkCredentials).length > 0;
  const pendingCommand = !isCommandFinal(command);
  const pageProps: PageProps | null = envelope && draft ? {
    draft, edit, envelope, deviceId: selectedDeviceId, onOpenConnections: openConnections,
    setServiceDetail, serviceDetail, setNetworkCredentials: (updater) => { formEditSequence.current++; setNetworkCredentials(updater); dirtyRef.current = true; setDirty(true); },
    networkCredentials, networkEditing, setNetworkEditing, updates, command, pendingCommand,
    ask: setConfirm, busy, save, dirty, identity: session.identity,
    refreshStatus: refreshSelectedStatus,
    downloadDiagnostics: async () => {
      const epoch = selectionEpoch.current;
      try {
        const file = await api.diagnostics(selectedDeviceId);
        if (selectedDeviceRef.current !== selectedDeviceId || selectionEpoch.current !== epoch) return;
        const url = URL.createObjectURL(file.blob); const link = document.createElement("a"); link.href = url; link.download = file.filename; link.click(); URL.revokeObjectURL(url);
      } catch (reason) { setAlert({ kind: "error", text: reason instanceof Error ? reason.message : "Диагностика недоступна" }); }
    },
    runAction: doAction,
  } : null;
  const statusPendingSince = envelope
    ? pendingSinceRef.current.get(`${envelope.device_id}:${envelope.revision}`) ?? Date.now()
    : Date.now();

  return (
    <div className="app-shell">
      <button className={`nav-scrim ${mobileNav ? "show" : ""}`} onClick={() => setMobileNav(false)} aria-label={t("Закрыть меню")} tabIndex={mobileNav ? 0 : -1} />
      <aside className={`sidebar ${mobileNav ? "open" : ""}`} inert={!mobileNav && window.matchMedia?.("(max-width: 760px)").matches ? true : undefined}>
        <div className="sidebar-brand"><strong>HomeDash</strong><span>{t("Настройки")}</span></div>
        <nav aria-label={t("Разделы настроек")}>
          {SECTION_LIST.map((item) => <button key={item.id} className={`nav-item ${!showConnections && section === item.id ? "active" : ""}`} aria-current={!showConnections && section === item.id ? "page" : undefined} onClick={() => navigate(item.id)}><Icon name={item.icon} /><span>{t(item.title)}</span></button>)}
          <button className={`nav-item global-nav ${showConnections ? "active" : ""}`} aria-current={showConnections ? "page" : undefined} onClick={openConnections}><Icon name="lock" /><span>{t("Общие подключения")}</span></button>
        </nav>
        <div className="sidebar-bottom">
          {selectedProfile && envelope && <div className="device-mini"><span className="device-mini-icon"><Icon name="monitor" size={18} /></span><div><strong>{envelope.settings.screen.name || selectedProfile.name || t("Дисплей")}</strong><span><StatusPill state={envelope.device.connected ? "good" : "warn"} label={envelope.device.connected ? t("На связи") : t("Не в сети")} /></span></div></div>}
          <button className="logout-button" onClick={logout}><Icon name="logout" size={17} /> {" "}{t("Завершить сеанс")}</button>
        </div>
      </aside>
      <main className="main-content">
        <header className="mobile-top"><button className="icon-button" aria-label={t("Открыть меню")} onClick={() => setMobileNav(true)}>☰</button><strong>HomeDash</strong></header>
        <div className="page-header">
          <div><h1>{t(showConnections ? "Общие подключения" : devices && devices.devices.length === 0 ? "Подключение дисплея" : current.title)}</h1><p>{t(showConnections ? "Параметры источников для всех дисплеев" : devices && devices.devices.length === 0 ? "Настройка первого дисплея" : current.subtitle)}</p></div>
          <div className="header-meta">
            <LanguageSelect />
            {devices && devices.devices.length > 0 && <label className="device-selector-label">{t("Дисплей")}<select className="device-selector" aria-label={t("Выбрать дисплей")} value={selectedDeviceId} disabled={busy} onChange={(event) => switchDevice(event.target.value)}>{devices.devices.map((device) => <option key={device.id} value={device.id}>{device.name || t("Дисплей")}{device.connected ? ` · ${t("На связи")}` : ` · ${t("Не в сети")}`}</option>)}</select></label>}
            {envelope && !showConnections && <StatusPill state={envelope.device.connected ? "good" : "warn"} label={envelope.device.connected ? t("Дисплей на связи") : t("Дисплей не в сети")} />}
          </div>
        </div>
        {envelope && !showConnections && <SettingsApplyStatusCard
          envelope={envelope}
          pendingSince={statusPendingSince}
          saveState={saveState}
          stale={statusStale}
          checking={checkingStatus}
          onCheck={() => void checkApplicationStatus()}
        />}
        {alert && <div className={`alert ${alert.kind}`} role={alert.kind === "error" ? "alert" : "status"}><span>{t(alert.text)}</span><button className="icon-button small" aria-label={t("Закрыть сообщение")} onClick={() => setAlert(null)}><Icon name="close" size={16} /></button></div>}
        {!devices && <div className="loading-screen"><div className="spinner" />{t("Загружаем дисплеи…")}</div>}
        {devices && showConnections && (connections ? <ConnectionsPage value={connections} onSave={saveConnections} onDirtyChange={(value) => { connectionsDirtyRef.current = value; }} /> : <div className="loading-screen"><div className="spinner" />{t("Загружаем общие подключения…")}</div>)}
        {devices && !showConnections && devices.devices.length === 0 && <EmptyDevices onOpenConnections={openConnections} />}
        {devices && !showConnections && devices.devices.length > 0 && !pageProps && <div className="loading-screen"><div className="spinner" />{t("Загружаем настройки дисплея…")}<button className="button secondary" onClick={() => selectedDeviceId && void loadDevice(selectedDeviceId).catch(() => undefined)}>{t("Повторить")}</button></div>}
        {pageProps && !showConnections && <>
          {conflict && <div className="alert error" role="alert"><span>{t("Настройки этого дисплея изменились в другом сеансе. Черновик сохранён.")}</span><div className="conflict-actions"><button className="button secondary" onClick={cancel}>{t("Загрузить актуальные и отменить черновик")}</button><button className="button primary" disabled={busy} onClick={() => { if (envelope) baseRevisionRef.current = envelope.revision; setConflict(false); void save(); }}>{t("Сохранить черновик поверх текущей версии")}</button></div></div>}
          <div className="page-body" ref={settingsFieldsRef}>
            {section === "screen" && <ScreenPage {...pageProps} />}
            {section === "photos" && <PhotosPage {...pageProps} />}
            {section === "services" && <ServicesPage {...pageProps} />}
            {section === "network" && <NetworkPage {...pageProps} />}
            {section === "updates" && <UpdatesPage {...pageProps} />}
            {section === "device" && <DevicePage {...pageProps} />}
          </div>
          {dirty && <div className="sticky-save"><span>{dirtySecrets ? t("Изменения не сохранены") : t("Есть несохранённые изменения")}</span><div><button className="button secondary" onClick={cancel} disabled={busy}>{t("Отменить")}</button><button className="button primary" onClick={() => void save()} disabled={busy}>{busy ? t("Сохранение…") : t("Сохранить изменения")}</button></div></div>}
          <footer className="page-footer">{t("Настройки сохраняются в Bridge. Применение на дисплее подтверждается отдельно.")}</footer>
        </>}
      </main>
      {confirm && <ConfirmDialog ask={confirm} busy={busy} onClose={() => setConfirm(null)} onConfirm={() => doAction(confirm.action)} />}
    </div>
  );
}

function EmptyDevices({ onOpenConnections }: { onOpenConnections: () => void }) {
  const t = useT();
  return <section className="empty-devices"><div className="brand-mark"><Icon name="monitor" size={30} /></div><h2>{t("Дисплеи ещё не подключены")}</h2><p>{t("Подключите и настройте HomeDash Bridge и дисплей. При первом доверенном обмене устройство появится здесь автоматически.")}</p><ol><li>{t("Запустите Bridge с включённой панелью управления.")}</li><li>{t("Настройте Wi-Fi и адрес Bridge в дисплее, используя ключ устройства из своей локальной конфигурации.")}</li><li>{t("После первой синхронизации обновите список. Ключ устройства не вводится и не хранится в браузере.")}</li></ol><button className="button secondary" onClick={() => window.location.reload()}>{t("Обновить список дисплеев")}</button><button className="button primary" onClick={onOpenConnections}>{t("Настроить общие подключения")}</button></section>;
}

function mergeSharedProjection(draft: Settings, latest: Settings): Settings {
  return {
    ...draft,
    photos: { ...draft.photos, base_url: latest.photos.base_url },
    services: {
      ...draft.services,
      calendar: { ...draft.services.calendar, base_url: latest.services.calendar.base_url },
      transport: { ...draft.services.transport, base_url: latest.services.transport.base_url },
      health: { ...draft.services.health, base_url: latest.services.health.base_url },
      news: { ...draft.services.news, base_url: latest.services.news.base_url },
      home: { ...draft.services.home, base_url: latest.services.home.base_url },
    },
  };
}
