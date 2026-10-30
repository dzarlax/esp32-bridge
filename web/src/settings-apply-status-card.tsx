import { useEffect, useState } from "react";
import type { Envelope } from "./types";
import { applicationState, hasCurrentApplicationAck } from "./settings-apply-status";
import { dateLocale, useLocale, useT } from "./i18n";

type Props = {
  envelope: Envelope;
  pendingSince: number;
  saveState: "idle" | "saving" | "saved" | "uncertain";
  stale: boolean;
  checking: boolean;
  onCheck: () => void;
};

export function SettingsApplyStatusCard({ envelope, pendingSince, saveState, stale, checking, onCheck }: Props) {
  const t = useT();
  const { locale } = useLocale();
  const [now, setNow] = useState(Date.now());
  const acknowledged = hasCurrentApplicationAck(envelope);
  const overdue = !acknowledged && now - pendingSince >= 45_000;
  const observedState = applicationState(envelope, pendingSince, now);
  const state = (saveState === "saving" || saveState === "uncertain") && observedState === "applied"
    ? "waiting"
    : observedState;

  useEffect(() => {
    if (state === "applied" || state === "not-confirmed") return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.max(0, pendingSince + 45_000 - Date.now()));
    return () => window.clearTimeout(timer);
  }, [state, pendingSince]);

  const applicationLabel = state === "applied"
    ? envelope.device.connected ? "Настройки подтверждены устройством" : "Настройки применены при последнем подключении"
    : state === "waiting-offline" ? "Ожидает подключения"
      : state === "not-confirmed" ? "Подтверждение не получено"
        : saveState === "saving" || saveState === "uncertain" ? "Проверяется текущая версия настроек" : "Ожидает применения дисплеем";

  return (
    <section className="apply-status-card" aria-label={t("Статус применения настроек")} aria-live="polite">
      <div className="apply-status-main">
        <div className="apply-status-heading">
          <strong>{t("Статус применения настроек")}</strong>
          <span className={`apply-status-indicator ${state}`} aria-hidden="true" />
        </div>
        <div className="apply-status-lines">
          {saveState !== "idle" && <span className="apply-bridge-status">
            {t(saveState === "saving" ? "Сохранение в Bridge…" : saveState === "saved" ? "Сохранено в Bridge" : "Результат сохранения не подтверждён")}
          </span>}
          <span className="apply-device-status">{t(applicationLabel)}</span>
          {state === "applied" && !envelope.device.connected && envelope.device.last_seen && <small>
            {t("Последнее подключение")}: {new Date(envelope.device.last_seen).toLocaleString(dateLocale(locale))}
          </small>}
          {overdue && <small>{t("Подтверждение не получено за 45 секунд. Bridge продолжает проверять статус без повторного сохранения.")}</small>}
          {saveState === "uncertain" && <small>{t("Ответ на сохранение не получен. Проверка читает состояние Bridge и не отправляет повторное сохранение. Записанные ключи нельзя проверить через настройки.")}</small>}
          {stale && <small role="status">{t("Не удалось обновить статус. Показано последнее известное состояние.")}</small>}
        </div>
      </div>
      {(stale || saveState === "uncertain" || overdue) && <button className="button secondary apply-check" onClick={onCheck} disabled={checking}>
        {checking ? t("Проверяем…") : t("Проверить статус")}
      </button>}
    </section>
  );
}
