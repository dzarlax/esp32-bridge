import { useEffect, useRef, useState } from "react";
import { BacklightStatus } from "./backlight-status";
import { useT } from "./i18n";
import type { PageProps } from "./page-types";
import type { Command } from "./types";
import { canRun, Field, SectionHeading, Select, TextInput, Toggle } from "./ui";

type Props = Pick<PageProps, "draft" | "edit" | "envelope" | "runAction" | "busy" | "pendingCommand" | "deviceObservationFresh">;
export function BacklightControls({ draft, edit, envelope, runAction, busy, pendingCommand, deviceObservationFresh }: Props) {
  const t = useT(); const device = envelope.device; const s = draft.screen;
  const live = device.capabilities.includes("backlight_adjust") && device.capabilities.includes("backlight_begin");
  const action = live ? "backlight_adjust" : "backlight_preview";
  const [candidate, setCandidate] = useState(50);
  const [desired, setDesired] = useState({ percent: 50, seq: 0 });
  const [sent, setSent] = useState(0);
  const [request, setRequest] = useState<{ command: Command; percent: number; seq: number } | null>(null);
  const [sending, setSending] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const ownedRestore = useRef<string | null>(null);
  const [blocked, setBlocked] = useState(false);
  const [notice, setNotice] = useState("");
  const [, setClock] = useState(0);
  const mounted = useRef(true); const inFlight = useRef(false);
  const report = device.backlight_report;
  const age = device.last_seen ? Date.now() - Date.parse(device.last_seen) : NaN;
  const remaining = report ? (report.restore_deadline_uptime_ms ?? 0) - report.sample_uptime_ms - age : 0;
  const fresh = deviceObservationFresh && device.connected && Number.isFinite(age) && age >= 0 && age <= 20000;
  useEffect(() => {
    if (desired.seq && !fresh) { setBlocked(true); setNotice("Нет свежего подтверждения подсветки"); }
  }, [desired.seq, fresh]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { const timer = window.setInterval(() => setClock(v => v + 1), 1000); return () => window.clearInterval(timer); }, []);
  useEffect(() => {
    if (!request || device.command?.id !== request.command.id) return;
    if (["failed", "unknown", "expired", "interrupted"].includes(device.command.state)) {
      setBlocked(true); setNotice("Проверка не подтверждена. Повторите её явно.");
    }
  }, [device.command, request]);
  useEffect(() => {
    if (!desired.seq || desired.seq <= sent || blocked || sending || busy || pendingCommand || !fresh || !canRun(device, action)) return;
    const timer = window.setTimeout(() => {
      if (inFlight.current) return;
      inFlight.current = true; setSending(true); setRequest(null); setNotice("Ожидаем подтверждения выбранной яркости");
      const nextAction = live && !request ? "backlight_begin" : action;
      void runAction(nextAction, desired.percent).then(command => {
        if (!mounted.current) return;
        setSent(desired.seq);
        if (command) setRequest({ command, percent: desired.percent, seq: desired.seq });
        else { setBlocked(true); setNotice("Проверка не подтверждена. Повторите её явно."); }
      }).catch(() => {
        if (mounted.current) { setSent(desired.seq); setBlocked(true); setNotice("Проверка не подтверждена. Повторите её явно."); }
      }).finally(() => { inFlight.current = false; if (mounted.current) setSending(false); });
    }, 500);
    return () => window.clearTimeout(timer);
  }, [desired, sent, blocked, sending, busy, pendingCommand, device, action, runAction, live, request, fresh]);
  const externalCommand = pendingCommand && !sending && !restoring && !inFlight.current && !!device.command && device.command.id !== request?.command.id && device.command.id !== ownedRestore.current;
  useEffect(() => { if (externalCommand) { setBlocked(true); setNotice("Другая команда выполняется. Повторите проверку после её завершения."); } }, [externalCommand]);
  const confirmed = !!(fresh && request && request.seq === desired.seq && request.percent === candidate && !sending && !busy && !pendingCommand &&
    device.command?.id === request.command.id && device.command.action === request.command.action && device.command.percent === candidate && device.command.state === "succeeded" &&
    report?.phase === "preview" && report.output_known && report.error === "" && remaining > 0);
  useEffect(() => {
    if (!blocked && fresh && request && device.command?.id === request.command.id && device.command.state === "succeeded" &&
        (report?.phase !== "preview" || remaining <= 0)) {
      setBlocked(true); setNotice("Проверка завершена. Для выбора повторите проверку.");
    }
  }, [blocked, fresh, request, device.command, report?.phase, remaining]);
  function change(value: number) {
    setCandidate(value);
    if (blocked || device.backlight_report?.phase !== "preview") setRequest(null);
    setBlocked(false); setNotice("");
    if (live) setDesired(previous => ({ percent: value, seq: previous.seq + 1 }));
  }
  function retry() { setBlocked(false); setRequest(null); setDesired(previous => ({ percent: candidate, seq: previous.seq + 1 })); }
  function assign(night: boolean) {
    if (!confirmed || !request) return;
    edit({ ...draft, screen: { ...s, [night ? "night_backlight_percent" : "backlight_percent"]: request.percent } });
    setBlocked(true); setRequest(null); setNotice("Уровень выбран в черновике. Дождитесь возврата и сохраните изменения.");
    setRestoring(true);
    void runAction("backlight_restore").then(command => { if (mounted.current) ownedRestore.current = command?.id ?? null; })
      .catch(() => { if (mounted.current) setNotice("Проверка не подтверждена. Повторите её явно."); })
      .finally(() => { if (mounted.current) setRestoring(false); });
  }
  const selectionRestored = blocked && !request && fresh && device.command?.id === ownedRestore.current && device.command.state === "succeeded" && report?.output_known && report.phase !== "preview";
  const set = (key: keyof typeof s, value: string | boolean) => edit({ ...draft, screen: { ...s, [key]: value } });
  return <section className="settings-section">
    <SectionHeading>{t("Яркость и ночной режим")}</SectionHeading>
    {!envelope.capabilities.brightness && <p>{t("Яркость недоступна до проверки и калибровки подсветки устройства.")}</p>}
    <BacklightStatus device={device} transportFresh={deviceObservationFresh ?? false} />
    {envelope.capabilities.brightness && <div className="backlight-picker">
      <Field label={t("Подбор яркости")}>
        <input aria-label={t("Подбор яркости")} type="range" min={10} max={100} value={candidate} disabled={externalCommand || !fresh || !canRun(device, action)} onChange={event => change(Number(event.target.value))} />
        <output>{candidate}%</output>
      </Field>
      <p className="field-note">{t("Проценты — шкала управления, не измеренный свет. Изменения временные.")}</p>
      <p className="field-note">{t("Ползунок отправляет последний выбранный уровень после паузы. Подтверждение зависит от связи с дисплеем.")}</p>
      <div className="inline-row">
        <button type="button" className="button secondary" disabled={busy || pendingCommand || !fresh || !canRun(device, action)} onClick={retry}>{t("Проверить на 20 секунд")}</button>
        <button type="button" className="button primary" disabled={!confirmed} onClick={() => assign(false)}>{t("Использовать днём")}</button>
        <button type="button" className="button primary" disabled={!confirmed} onClick={() => assign(true)}>{t("Использовать ночью")}</button>
      </div>
      <p role="status">{confirmed ? t("Дисплей подтвердил этот уровень. Можно выбрать день или ночь.") : selectionRestored ? t("Уровень выбран в черновике. Можно сохранить изменения.") : request && !blocked && fresh && remaining <= 0 ? t("Проверка завершена. Для выбора повторите проверку.") : notice ? t(notice) : t("Выберите яркость и дождитесь подтверждения дисплея.")}</p>
    </div>}
      <p>{t("Дневная яркость")}: <strong>{s.backlight_percent === undefined ? t("Не выбрана") : `${s.backlight_percent}%`}</strong> · {t("Ночная яркость")}: <strong>{s.night_backlight_percent === undefined ? t("Не выбрана") : `${s.night_backlight_percent}%`}</strong></p>
      {(s.backlight_percent !== undefined || s.night_backlight_percent !== undefined) && <button type="button" className="button secondary" onClick={() => {
        const next = { ...s, night_action: "off" as const }; delete next.backlight_percent; delete next.night_backlight_percent;
        edit({ ...draft, screen: next });
      }}>{t("Сбросить выбранные уровни")}</button>}
    <Field label={t("Ночной режим")}>
      <Toggle label={s.night_enabled ? t("Ночной режим включён") : t("Ночной режим выключен")} checked={s.night_enabled} onChange={value => set("night_enabled", value)} />
      <div className="inline-row">
        <TextInput type="time" aria-label={t("Начало ночного режима")} value={s.night_start} onChange={event => set("night_start", event.target.value)} disabled={!s.night_enabled} />
        <span>—</span>
        <TextInput type="time" aria-label={t("Конец ночного режима")} value={s.night_end} onChange={event => set("night_end", event.target.value)} disabled={!s.night_enabled} />
      </div>
      <small>{t("Касание разбудит экран.")}</small>
    </Field>
    <Field label={t("Ночное действие")}>
      <Select value={s.night_action || "off"} onChange={event => set("night_action", event.target.value)}>
        <option value="off">{t("Выключить экран")}</option>
        <option value="dim" disabled={!envelope.capabilities.brightness || s.backlight_percent === undefined || s.night_backlight_percent === undefined}>{t("Уменьшить яркость")}</option>
      </Select>
      <small>{s.backlight_percent === undefined || s.night_backlight_percent === undefined ? t("Для приглушения сначала выберите дневную и ночную яркость.") : t("Уровни используются по локальному времени дисплея.")}</small>
    </Field>
    {device.backlight_error && <p>{t("Устройство сообщает об ошибке подсветки.")}</p>}
    {device.capabilities.includes("backlight_restore") && <button type="button" className="button secondary" disabled={busy || pendingCommand || !canRun(device, "backlight_restore")} onClick={() => void runAction("backlight_restore")}>{t("Восстановить яркость")}</button>}
  </section>;
}
