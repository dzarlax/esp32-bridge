import { useEffect, useRef, useState } from "react";
import { AdminApi } from "./api";
import { dateLocale, useLocale, useT } from "./i18n";

export interface CalendarSource {
  id: string;
  state: string;
  stale?: boolean;
  last_success_at?: string | null;
  next_sync_at?: string | null;
  coverage_complete?: boolean | null;
  error_code?: string;
  reason: string;
  reasons?: string[];
}
export interface CalendarDetails {
  date: string;
  complete: boolean;
  reason: string;
  sources: CalendarSource[];
}

const api = new AdminApi();
const messages: Record<string, string> = {
  complete: "Расписание полное",
  ready: "Данные актуальны",
  cache_stale: "Данные календаря ожидают обновления",
  coverage_incomplete: "Покрытие запрошенного периода неполное",
  source_incomplete: "Данные источника неполные",
  source_error: "Источник сообщил об ошибке",
  source_pending: "Источник ещё не готов",
  incomplete: "Расписание может быть неполным",
  unknown: "Причина неполноты неизвестна",
  disabled: "Календарь выключен",
};
const errorMessages: Record<string, string> = {
  auth: "Требуется проверка авторизации календаря",
  permission: "Нет прав на чтение календаря",
  rate_limited: "Сервис ограничивает частоту запросов",
  transient: "Временная ошибка синхронизации",
  unsupported: "Источник не поддерживает синхронизацию",
  protocol: "Ошибка ответа календарного сервиса",
};

function formatSyncTime(
  value: string | null | undefined,
  t: (key: string) => string,
  locale: "ru" | "en" | "sr-Latn",
  rejectFuture: boolean,
): string {
  if (!value) return t("Время синхронизации неизвестно");
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return t("Время синхронизации неизвестно");
  if (rejectFuture && date.getTime() > Date.now()) return t("Возможен сдвиг часов");
  // Date formatting uses the browser's current timezone; the display timezone is not available here.
  return new Intl.DateTimeFormat(dateLocale(locale), { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function CalendarStatus({ deviceId }: { deviceId: string }) {
  const t = useT();
  const { locale } = useLocale();
  const [details, setDetails] = useState<CalendarDetails | null>(null);
  const [detailsOwner, setDetailsOwner] = useState("");
  const [names, setNames] = useState<Record<string, string>>({});
  const [namesOwner, setNamesOwner] = useState("");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const request = useRef<AbortController | null>(null);

  useEffect(() => {
    request.current?.abort();
    request.current = null;
    setDetails(null);
    setDetailsOwner("");
    setNames({});
    setNamesOwner("");
    setFailed(false);
    setBusy(false);
    return () => request.current?.abort();
  }, [deviceId]);

  async function refresh() {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setFailed(false);
    try {
      const data = await api.calendarStatus(deviceId, controller.signal);
      if (controller.signal.aborted) return;
      if (!Array.isArray(data.sources) || typeof data.complete !== "boolean") throw Error("invalid response");
      setDetails(data);
      setDetailsOwner(deviceId);
      try {
        const catalog = await api.catalog(deviceId, "calendars");
        if (!controller.signal.aborted) {
          setNames(Object.fromEntries(catalog.items.map((item) => [item.id, item.name])));
          setNamesOwner(deviceId);
        }
      } catch {
        // Status remains useful if the catalog is temporarily unavailable.
      }
    } catch {
      if (!controller.signal.aborted) setFailed(true);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  return (
    <div className="status-card">
      <p>{t("Состояние сохранённых календарей")}</p>
      <button className="button secondary" type="button" disabled={busy} onClick={() => void refresh()}>
        {t(busy ? "Проверяю календари…" : "Проверить календари")}
      </button>
      {failed && <p role="alert">{t("Не удалось проверить календари. Повторите запрос.")}</p>}
      {details && detailsOwner === deviceId && (
        <div aria-live="polite">
          <p>{t(messages[details.reason] || messages.unknown)}{details.date ? ` · ${details.date}` : ""}</p>
          <ul>
            {details.sources.map((source, index) => {
              const reasons = source.reasons?.length ? source.reasons : [source.reason];
              const errorMessage = errorMessages[source.error_code || ""];
              const visibleMessages = reasons
                .filter((reason) => !(reason === "source_error" && errorMessage))
                .map((reason) => messages[reason] || messages.unknown);
              if (errorMessage) visibleMessages.push(errorMessage);
              const name = (namesOwner === deviceId ? names[source.id] : undefined)
                || `${t("Календарь")} ${index + 1}`;
              return (
                <li key={source.id}>
                  {name} — {visibleMessages.map((message, reasonIndex) => (
                    <span key={`${message}-${reasonIndex}`}>{reasonIndex ? " · " : ""}{t(message)}</span>
                  ))}
                  <small>{` · ${t("Последнее обновление")}: ${formatSyncTime(source.last_success_at, t, locale, true)} · ${t("Следующая синхронизация")}: ${formatSyncTime(source.next_sync_at, t, locale, false)}`}</small>
                </li>
              );
            })}
          </ul>
          <small>{t("Статусы относятся к сохранённому выбору. Обновление страницы не запускает синхронизацию провайдера.")}</small>
        </div>
      )}
    </div>
  );
}
