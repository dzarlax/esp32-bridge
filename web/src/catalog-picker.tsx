import { useEffect, useState } from "react";
import { AdminApi, ApiError } from "./api";
import { useT } from "./i18n";
import type { CatalogItem } from "./types";

const api = new AdminApi();
const errors: Record<string, string> = {
  catalog_connection_changed: "Подключение изменилось. Обновите список.",
  catalog_not_configured: "Настройте сохранённое подключение к источнику.",
  catalog_forbidden: "Источник отказал в доступе к списку.",
  catalog_api_unavailable: "Источник не поддерживает список в этой версии.",
  catalog_timeout: "Источник не ответил вовремя.",
  catalog_too_large: "Список слишком велик для загрузки.",
  catalog_upstream_error: "Не удалось получить список от источника.",
  catalog_invalid_response: "Источник вернул некорректный список.",
};
export function CatalogPicker({ deviceId, kind, selected, onChange }: { deviceId: string; kind: "albums" | "calendars"; selected: string[]; onChange: (ids: string[]) => void }) {
  const t = useT();
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [query, setQuery] = useState("");
  useEffect(() => {
    let active = true;
    setState("loading");
    api.catalog(deviceId, kind).then((result) => {
      if (!active) return;
      if (!result.complete || !Array.isArray(result.items)) throw new ApiError("catalog_invalid_response", 502);
      setItems(result.items); setState("ready");
    }).catch((reason: unknown) => {
      if (!active) return;
      setError(reason instanceof ApiError ? errors[reason.code] ?? "Не удалось загрузить список." : "Не удалось загрузить список."); setState("error");
    });
    return () => { active = false; };
  }, [deviceId, kind, attempt]);
  const missing = state === "ready" ? selected.filter((id) => !items.some((item) => item.id === id)) : selected;
  const filtered = items.filter((item) => `${item.name} ${item.id}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
  return <div className="catalog-picker">
    {state === "loading" && <p role="status">{t("Загрузка списка…")}</p>}
    {state === "error" && <p role="alert">{t(error)} <button type="button" className="button secondary" onClick={() => setAttempt((value) => value + 1)}>{t("Повторить")}</button></p>}
    {state === "ready" && <>
      <div className="catalog-controls"><input type="search" aria-label={t("Поиск по имени или ID")} placeholder={t("Поиск по имени или ID")} value={query} onChange={(event) => setQuery(event.target.value)} /><button type="button" className="button secondary" onClick={() => setAttempt((value) => value + 1)}>{t("Обновить список")}</button></div>
      {!items.length && <p>{t("Список пуст. Текущий выбор сохранён.")}</p>}
      {items.length > 0 && !filtered.length && <p>{t("Ничего не найдено.")}</p>}
      <div className="catalog-list">{filtered.map((item) => <label className="catalog-option" key={item.id}>
        <input type={kind === "albums" ? "radio" : "checkbox"} name={kind === "albums" ? `album-${deviceId}` : undefined} checked={selected.includes(item.id)} onChange={(event) => onChange(kind === "albums" ? [item.id] : event.target.checked ? [...selected, item.id] : selected.filter((id) => id !== item.id))} />
        <span>{item.name}<small>{item.id}</small>{item.asset_count !== undefined && <small>{t("Элементов")}: {item.asset_count}</small>}{kind === "calendars" && <small>{[item.provider, item.time_zone, t("только чтение")].filter(Boolean).join(" · ")}</small>}</span>
      </label>)}</div>
    </>}
    {missing.map((id) => <div className="catalog-retained" key={id}><span>{id} — {t(state === "ready" ? "Недоступен в списке" : "Сохранённый выбор; список не загружен")}</span> <button type="button" className="button secondary" onClick={() => onChange(selected.filter((value) => value !== id))}>{t("Удалить из выбора")}</button></div>)}
  </div>;
}
