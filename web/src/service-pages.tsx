import { CalendarStatus } from "./calendar-status";
import { CatalogPicker } from "./catalog-picker";
import { AdminApi, ApiError } from "./api";
import type { CatalogItem, Favorite } from "./types";
import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import type { Settings } from "./types";
import { errorMessage } from "./error-messages";
import type { PageProps } from "./page-types";
import {
  appliedSettingsStatus,
  authMethodLabel,
  canRun,
  Field,
  Icon,
  SectionHeading,
  Select,
  servicePill,
  StatusPill,
  TextInput,
  Toggle,
  tzLabel,
} from "./ui";
import { dateLocale, useLocale, useT } from "./i18n";

function ServiceIcon({ name }: { name: keyof Settings["services"] }) {
  const map = {
    weather: "sun",
    calendar: "calendar",
    transport: "train",
    health: "heart",
    tasks: "tasks",
    news: "news",
    home: "home",
  } as const;
  return <Icon name={map[name]} />;
}
const serviceInfo: Record<
  keyof Settings["services"],
  { title: string; subtitle: string }
> = {
  weather: { title: "Погода", subtitle: "Open-Meteo" },
  calendar: { title: "Календарь", subtitle: "Calendar Platform" },
  transport: { title: "Транспорт", subtitle: "Избранные остановки" },
  health: { title: "Здоровье", subtitle: "Health Dashboard" },
  tasks: { title: "Задачи", subtitle: "Todoist" },
  news: { title: "Новости", subtitle: "Лента и краткие сводки" },
  home: { title: "Умный дом", subtitle: "Home Assistant" },
};

function ServiceEditor({
  id,
  props,
  titleId,
}: {
  id: keyof Settings["services"];
  props: PageProps;
  titleId?: string;
}) {
  const t = useT();
  const { draft, edit, envelope } = props;
  const item = draft.services[id] as unknown as {
    enabled: boolean;
    latitude: number;
    longitude: number;
    timezone: string;
    base_url: string;
    ids: string[];
    limit: number;
    since_hours: number;
    sensors: string[];
    lights: string[];
    climates: string[];
  };
  const update = (field: string, value: unknown) =>
    edit({
      ...draft,
      services: {
        ...draft.services,
        [id]: { ...item, [field]: value },
      } as Settings["services"],
    });
  const list = (field: string, value: string) =>
    update(
      field,
      value
        .split(/[\n,]+/)
        .map((part) => part.trim())
        .filter(Boolean),
    );
  return (
    <div className="service-editor">
      <div className="service-editor-heading">
        <span className="service-large-icon">
          <ServiceIcon name={id} />
        </span>
        <div>
          <h2 id={titleId}>{t(serviceInfo[id].title)}</h2>
          <p>{t(serviceInfo[id].subtitle)}</p>
        </div>
      </div>
      {id === "weather" && (
        <>
          <Field label={t("Широта")}>
            <TextInput
              type="number"
              min={-90}
              max={90}
              step="any"
              value={item.latitude}
              onChange={(e) => update("latitude", Number(e.target.value))}
            />
          </Field>
          <Field label={t("Долгота")}>
            <TextInput
              type="number"
              min={-180}
              max={180}
              step="any"
              value={item.longitude}
              onChange={(e) => update("longitude", Number(e.target.value))}
            />
          </Field>
          <Field label={t("Часовой пояс")}>
            <Timezone
              label={t("Часовой пояс погоды")}
              value={item.timezone}
              options={envelope.capabilities.timezones}
              onChange={(value) => update("timezone", value)}
            />
          </Field>
        </>
      )}
      {id === "calendar" && (
        <>
          <SharedConnectionField
            label="Calendar Platform"
            value={item.base_url}
            onEdit={props.onOpenConnections}
          />
          <Field label={t("Календари")}>
            <CatalogPicker deviceId={props.deviceId} kind="calendars" selected={item.ids} onChange={(ids) => update("ids", ids)} />
            <CalendarStatus key={props.deviceId} deviceId={props.deviceId} />
          </Field>
          <details><summary>{t("Дополнительно: ID вручную")}</summary>
            <Field label={t("Календари")} help={t("ID календарей, по одному на строку или через запятую.")}><textarea rows={3} value={item.ids.join("\n")} onChange={(e) => list("ids", e.target.value)} /></Field>
          </details>
          <Field label={t("Часовой пояс")}>
            <Timezone
              label={t("Часовой пояс календаря")}
              value={item.timezone}
              options={envelope.capabilities.timezones}
              onChange={(value) => update("timezone", value)}
            />
          </Field>
        </>
      )}
      {id === "transport" && (
        <>
          <SharedConnectionField
            label={t("Источник транспорта")}
            value={item.base_url}
            onEdit={props.onOpenConnections}
          />
          <Field
            label={t("Остановки")}
            help={t("ID остановок, по одному на строку или через запятую.")}
          >
            <textarea
              rows={3}
              value={item.ids.join("\n")}
              onChange={(e) => list("ids", e.target.value)}
            />
          </Field>
        </>
      )}
      {id === "health" && (
        <>
          <SharedConnectionField
            label="Health Dashboard"
            value={item.base_url}
            onEdit={props.onOpenConnections}
          />
        </>
      )}
      {id === "tasks" && (
        <SharedConnectionField
          label="Todoist"
          value={envelope.secrets_present.todoist_token ? t("Ключ сохранён") : ""}
          onEdit={props.onOpenConnections}
        />
      )}
      {id === "news" && (
        <>
          <SharedConnectionField
            label={t("Источник новостей")}
            value={item.base_url}
            onEdit={props.onOpenConnections}
          />
          <Field label={t("Количество элементов")}>
            <TextInput
              type="number"
              min={1}
              max={50}
              value={item.limit}
              onChange={(e) => update("limit", Number(e.target.value))}
            />
          </Field>
          <Field label={t("Период новостей")}>
            <TextInput
              type="number"
              min={1}
              max={720}
              value={item.since_hours}
              onChange={(e) => update("since_hours", Number(e.target.value))}
            />
          </Field>
        </>
      )}
      {id === "home" && (
        <>
          <SharedConnectionField
            label="Home Assistant"
            value={item.base_url}
            onEdit={props.onOpenConnections}
          />
          <FavoritesEditor home={draft.services.home} supported={envelope.device.capabilities.includes("home_favorites")} onChange={(favorites) => update("favorites", favorites)} />
          <HomeEntityPicker deviceId={props.deviceId} selected={{ light: item.lights, climate: item.climates, sensor: item.sensors }} onChange={(next) => edit({ ...draft, services: { ...draft.services, home: { ...draft.services.home, lights: next.light, climates: next.climate, sensors: next.sensor } } })} />
          <details><summary>{t("Дополнительно: ID вручную")}</summary>{(["sensors", "lights", "climates"] as const).map((field) => (
            <Field
              key={field}
              label={
                { sensors: "Датчики", lights: "Свет", climates: "Климат" }[
                  field
                ]
              }
              help={t("ID сущностей, по одному на строку или через запятую.")}
            >
              <textarea
                rows={3}
                value={item[field].join("\n")}
                onChange={(e) => list(field, e.target.value)}
              />
            </Field>
          ))}</details>
        </>
      )}
    </div>
  );
}

export function HomeEntityPicker({ selected, onChange, deviceId }: { selected: Record<"light" | "climate" | "sensor", string[]>; onChange: (value: Record<"light" | "climate" | "sensor", string[]>) => void; deviceId: string }) {
  const t = useT();
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [domain, setDomain] = useState<"light" | "climate" | "sensor">("light");
  const [query, setQuery] = useState("");
  useEffect(() => {
    let active = true;
    setState("loading");
    new AdminApi().entities(deviceId).then((result) => {
      if (!active) return;
      if (!result.complete || !Array.isArray(result.items)) throw new ApiError("catalog_invalid_response", 502);
      setItems(result.items); setState("ready");
    }).catch((reason: unknown) => {
      if (!active) return;
      const codes: Record<string, string> = { catalog_connection_changed: "Подключение изменилось. Обновите список.", catalog_not_configured: "Настройте сохранённое подключение к источнику.", catalog_forbidden: "Источник отказал в доступе к списку.", catalog_timeout: "Источник не ответил вовремя.", catalog_too_large: "Список слишком велик для загрузки.", catalog_upstream_error: "Не удалось получить список от источника.", catalog_invalid_response: "Источник вернул некорректный список." };
      setError(t(reason instanceof ApiError ? codes[reason.code] ?? "Не удалось загрузить список." : "Не удалось загрузить список.")); setState("error");
    });
    return () => { active = false; };
  }, [deviceId, attempt, t]);
  const allSelected = ["light", "climate", "sensor"].flatMap((kind) => selected[kind as keyof typeof selected]);
  const missing = state === "ready" ? allSelected.filter((id) => !items.some((item) => item.id === id)) : allSelected;
  const filtered = items.filter((item) => item.domain === domain && `${item.name} ${item.id}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
  const toggle = (item: CatalogItem, checked: boolean) => {
    if (!item.domain) return;
    const current = selected[item.domain];
    const next = checked ? [...current, item.id] : current.filter((id) => id !== item.id);
    onChange({ ...selected, [item.domain]: next });
  };
  return <div className="catalog-picker home-entity-picker field-control">
    <strong>{t("Выбор сущностей Home Assistant")}</strong>
    {state === "loading" && <p role="status">{t("Загрузка списка…")}</p>}
    {state === "error" && <p role="alert">{error} <button type="button" className="button secondary" onClick={() => setAttempt((value) => value + 1)}>{t("Повторить")}</button></p>}
    {state === "ready" && <>
      <div className="catalog-controls"><input type="search" aria-label={t("Поиск по имени или ID")} placeholder={t("Поиск по имени или ID")} value={query} onChange={(event) => setQuery(event.target.value)} /><button type="button" className="button secondary" onClick={() => setAttempt((value) => value + 1)}>{t("Обновить список")}</button></div>
      <div role="tablist" aria-label={t("Тип сущности")} className="entity-tabs">{(["light", "climate", "sensor"] as const).map((kind) => <button type="button" role="tab" aria-selected={domain === kind} key={kind} onClick={() => setDomain(kind)}>{t(({ light: "Свет", climate: "Климат", sensor: "Датчики" } as const)[kind])}</button>)}</div>
      {!filtered.length && <p>{t(items.length ? "Ничего не найдено." : "Список пуст. Текущий выбор сохранён.")}</p>}
      <div className="catalog-list">{filtered.map((item) => { const current = selected[item.domain!]; const limit = ({ light: 8, climate: 1, sensor: 10 } as const)[item.domain!]; const entityIDTooLong = new TextEncoder().encode(item.id).length > (item.domain === "sensor" ? 128 : 47); return <label className="catalog-option" key={item.id}>
        <input type="checkbox" checked={current.includes(item.id)} disabled={(item.available === false && !current.includes(item.id)) || (entityIDTooLong && !current.includes(item.id)) || (!current.includes(item.id) && current.length >= limit)} onChange={(event) => toggle(item, event.target.checked)} />
        <span>{item.name}<small>{item.id}</small>{item.available === false && <small>{t("Недоступна в Home Assistant")}</small>}{entityIDTooLong && <small>{t("ID сущности превышает ограничение устройства")}</small>}</span>
      </label>; })}</div>
    </>}
    {missing.map((id) => <div className="catalog-retained" key={id}><span>{id} — {t(state === "ready" ? "Недоступен в списке" : "Сохранённый выбор; список не загружен")}</span><button type="button" className="button secondary" onClick={() => { const key = id.startsWith("light.") ? "light" : id.startsWith("climate.") ? "climate" : "sensor"; onChange({ ...selected, [key]: selected[key].filter((value) => value !== id) }); }}>{t("Удалить из выбора")}</button></div>)}
  </div>;
}

function FavoritesEditor({ home, supported, onChange }: { home: Settings["services"]["home"]; supported: boolean; onChange: (value: Favorite[]) => void }) {
  const t = useT();
  const favorites = home.favorites ?? [];
  const choices = [...home.lights.map((entity_id) => ({ entity_id, kind: "light" as const })), ...home.climates.map((entity_id) => ({ entity_id, kind: "climate" as const }))].filter((entry) => !favorites.some((favorite) => favorite.entity_id === entry.entity_id));
  const move = (index: number, offset: number) => { const next = [...favorites]; const target = index + offset; if (target < 0 || target >= next.length) return; [next[index], next[target]] = [next[target]!, next[index]!]; onChange(next); };
  return <div className="favorites-editor field-control"><strong>{t("Избранное на главном экране")}</strong><p className="field-note">{t("До четырёх настроенных источников света или климата. Порядок задаётся кнопками.")}</p>
    {!supported && <p>{t("Эта прошивка не поддерживает избранное.")}</p>}
    {favorites.map((favorite, index) => { const available = (favorite.kind === "light" ? home.lights : home.climates).includes(favorite.entity_id); return <div className="favorite-item" key={favorite.entity_id}>
      <span>{favorite.entity_id} { !available && <small>— {t("Недоступен в настройках")}</small>}</span>
      <input aria-label={`${t("Подпись")} ${favorite.entity_id}`} value={favorite.label ?? ""} disabled={!supported || !available} onChange={(e) => { const next = [...favorites]; const label = e.target.value; if (new TextEncoder().encode(label).length > 32 || /[\x00-\x1f\x7f]/.test(label)) return; next[index] = { ...favorite, label }; onChange(next); }} />
      <button type="button" disabled={!supported || index === 0} aria-label={`${t("Переместить выше")} ${favorite.entity_id}`} onClick={() => move(index, -1)}>↑</button>
      <button type="button" disabled={!supported || index === favorites.length - 1} aria-label={`${t("Переместить ниже")} ${favorite.entity_id}`} onClick={() => move(index, 1)}>↓</button>
      <button type="button" disabled={!supported} onClick={() => onChange(favorites.filter((_, i) => i !== index))}>{t("Удалить")}</button>
    </div>; })}
    <select aria-label={t("Добавить в избранное")} disabled={!supported || favorites.length >= 4 || !choices.length} value="" onChange={(e) => { const choice = choices.find((entry) => entry.entity_id === e.target.value); if (choice && new TextEncoder().encode(choice.entity_id).length <= 47) onChange([...favorites, choice]); }}><option value="">{t("Добавить в избранное")}</option>{choices.map((entry) => <option key={entry.entity_id} value={entry.entity_id}>{entry.entity_id}</option>)}</select>
  </div>;
}

function Timezone({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}) {
  const t = useT();
  return (
    <Select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((tz) => (
        <option key={tz} value={tz}>
          {tzLabel(tz, t)}
        </option>
      ))}
    </Select>
  );
}

function SharedConnectionField({
  label,
  value,
  onEdit,
}: {
  label: string;
  value: string;
  onEdit: () => void;
}) {
  const t = useT();
  return (
    <Field
      label={label}
      help={t("Общее подключение для всех дисплеев. Изменить его можно в разделе «Общие подключения».")}
    >
      <div className="shared-connection-field">
        <TextInput value={value || t("Не настроено")} readOnly aria-label={`${label}, ${t("только чтение")}`} />
        <button className="button secondary" type="button" onClick={onEdit}>
          {" "}{t("Общие подключения")}{" "}</button>
      </div>
    </Field>
  );
}

export function ServicesPage(props: PageProps) {
  const t = useT();
  const { locale } = useLocale();
  const { draft, edit, envelope, serviceDetail, setServiceDetail } = props;
  const [mobileEditorOpen, setMobileEditorOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const items = Object.keys(serviceInfo) as (keyof Settings["services"])[];
  function selectService(
    id: keyof Settings["services"],
    trigger: HTMLButtonElement,
  ) {
    setServiceDetail(id);
    if (window.matchMedia?.("(max-width: 760px)").matches) {
      triggerRef.current = trigger;
      setMobileEditorOpen(true);
    }
  }
  const closeMobileEditor = useCallback(() => setMobileEditorOpen(false), []);
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 760px)");
    if (!media?.addEventListener) return;
    const onViewportChange = (event: MediaQueryListEvent) => {
      if (!event.matches) setMobileEditorOpen(false);
    };
    media.addEventListener("change", onViewportChange);
    return () => media.removeEventListener("change", onViewportChange);
  }, []);
  return (
    <div className="services-layout">
      <div className="service-list" role="list" aria-label={t("Сервисы")}>
        {items.map((id) => {
          const setting = draft.services[id] as { enabled: boolean };
          const status = envelope.statuses[id];
          return (
            <div
              className={`service-row ${serviceDetail === id ? "selected" : ""}`}
              role="listitem"
              key={id}
            >
              <button
                className="service-select"
                onClick={(event) => selectService(id, event.currentTarget)}
                aria-current={serviceDetail === id ? "true" : undefined}
              >
                <ServiceIcon name={id} />
                <span>
                  <strong>{t(serviceInfo[id].title)}</strong>
                  <small>{t(serviceInfo[id].subtitle)}</small>
                </span>
                <span className="service-status">
                  {servicePill(status, t)}
                  {status?.last_success && (
                    <small>
                      {" "}{t("Последний ответ")}{" "}
                      {new Date(status.last_success).toLocaleString(dateLocale(locale))}
                    </small>
                  )}
                  {status?.error && (
                    <small className="error-text">{t(errorMessage(status.error, "Не удалось получить данные. Проверьте подключение к сервису в разделе «Общие подключения».") )}</small>
                  )}
                </span>
              </button>
              <Toggle
                label={t("Показывать")}
                checked={setting.enabled}
                onChange={(value) =>
                  edit({
                    ...draft,
                    services: {
                      ...draft.services,
                      [id]: { ...setting, enabled: value },
                    } as Settings["services"],
                  })
                }
              />
              <button
                className="service-config"
                onClick={(event) => selectService(id, event.currentTarget)}
              >
                {" "}{t("Настроить")}{" "}<span aria-hidden="true">›</span>
              </button>
            </div>
          );
        })}
      </div>
      <aside className="service-detail">
        <ServiceEditor id={serviceDetail} props={props} />
        <ServiceVisibility id={serviceDetail} props={props} />
      </aside>
      {mobileEditorOpen && (
        <ServiceSheet
          id={serviceDetail}
          props={props}
          triggerRef={triggerRef}
          onClose={closeMobileEditor}
        />
      )}
    </div>
  );
}

function ServiceVisibility({
  id,
  props,
}: {
  id: keyof Settings["services"];
  props: PageProps;
}) {
  const t = useT();
  const setting = props.draft.services[id] as { enabled: boolean };
  return (
    <div className="service-visibility">
      <Toggle
        label={t("Показывать на дисплее")}
        checked={setting.enabled}
        onChange={(value) =>
          props.edit({
            ...props.draft,
            services: {
              ...props.draft.services,
              [id]: { ...props.draft.services[id], enabled: value },
            } as Settings["services"],
          })
        }
      />
    </div>
  );
}

function ServiceSheet({
  id,
  props,
  triggerRef,
  onClose,
}: {
  id: keyof Settings["services"];
  props: PageProps;
  triggerRef: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
}) {
  const t = useT();
  const dialogRef = useRef<HTMLElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const [saveError, setSaveError] = useState("");

  useEffect(() => {
    const previousFocus = triggerRef.current;
    closeRef.current?.focus();
    const dialog = dialogRef.current;
    const selector =
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab" || !dialog) return;
      const controls = Array.from(
        dialog.querySelectorAll<HTMLElement>(selector),
      );
      if (controls.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      previousFocus?.focus();
    };
  }, [onClose, triggerRef]);

  async function saveAndClose() {
    setSaveError("");
    const error = await props.save();
    if (error) {
      setSaveError(error);
      return;
    }
    onClose();
  }

  return (
    <div
      className="service-sheet-backdrop"
      role="presentation"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        className="service-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="mobile-service-title"
        ref={dialogRef}
        tabIndex={-1}
      >
        <div className="service-sheet-header">
          <span>{t("Настройки сервиса")}</span>
          <button
            ref={closeRef}
            className="icon-button"
            aria-label={t("Закрыть настройки сервиса")}
            onClick={onClose}
          >
            <Icon name="close" size={20} />
          </button>
        </div>
        <div className="service-sheet-content">
          <ServiceEditor
            id={id}
            props={props}
            titleId="mobile-service-title"
          />
          <ServiceVisibility id={id} props={props} />
          {saveError && (
            <div className="alert error" role="alert">
              {t(saveError)}
            </div>
          )}
        </div>
        <footer className="service-sheet-footer">
          <span>
            {props.dirty
              ? t("Есть несохранённые изменения")
              : t("Изменений нет")}
          </span>
          <div>
            <button className="button secondary" onClick={onClose}>
              {" "}{t("Закрыть")}{" "}</button>
            <button
              className="button primary"
              disabled={!props.dirty || props.busy}
              onClick={() => void saveAndClose()}
            >
              {props.busy ? t("Сохранение…") : t("Сохранить изменения")}
            </button>
          </div>
        </footer>
      </section>
    </div>
  );
}

export function NetworkPage({
  draft,
  edit,
  envelope,
  networkCredentials,
  setNetworkCredentials,
  networkEditing,
  setNetworkEditing,
  identity,
}: PageProps) {
  const t = useT();
  const change = (value: string) =>
    edit({ ...draft, network: { bridge_url: value } });
  const applyStatus = appliedSettingsStatus(envelope, t);
  return (
    <div className="content-narrow">
      <section className="settings-section">
        <SectionHeading
          detail={
            <StatusPill
              state={envelope.device.connected ? "good" : "warn"}
              label={envelope.device.connected ? t("Подключён") : t("Нет связи")}
            />
          }
        >
          {" "}{t("Wi-Fi дисплея")}{" "}</SectionHeading>
        <div className="readout">
          <span>{t("Текущая сеть (SSID)")}</span>
          <strong>{envelope.device.ssid || t("Не сообщается")}</strong>
        </div>
        <div className="readout">
          <span>{t("IP-адрес")}</span>
          <strong>{envelope.device.ip || t("Не сообщается")}</strong>
        </div>
        {!networkEditing && (
          <button
            className="button secondary"
            onClick={() => setNetworkEditing(true)}
            disabled={!canRun(envelope.device, "network")}
          >
            {" "}{t("Изменить сеть")}{" "}</button>
        )}
        {networkEditing && (
          <div className="nested-form">
            <Field label={t("Новая сеть Wi-Fi")}>
              <TextInput
                value={networkCredentials.wifi_ssid ?? ""}
                autoComplete="off"
                onChange={(e) => {
                  const value = e.target.value;
                  setNetworkCredentials((old) => ({
                    ...old,
                    wifi_ssid: value,
                  }));
                }}
              />
            </Field>
            <Field
              label={t("Пароль Wi-Fi")}
              help={t("Оставьте пустым, чтобы не менять пароль.")}
            >
              <TextInput
                type="password"
                autoComplete="new-password"
                value={networkCredentials.wifi_password ?? ""}
                onChange={(e) => {
                  const value = e.target.value;
                  setNetworkCredentials((old) => ({
                    ...old,
                    wifi_password: value,
                  }));
                }}
              />
            </Field>
          </div>
        )}
      </section>
      <section className="settings-section">
        <SectionHeading
          detail={
            <StatusPill
              state={envelope.device.connected ? "good" : "warn"}
              label={envelope.device.connected ? t("На связи") : t("Не в сети")}
            />
          }
        >
          Bridge
        </SectionHeading>
        <Field label={t("Адрес Bridge")}>
          <TextInput
            type="url"
            value={draft.network.bridge_url}
            placeholder="https://bridge.example.com"
            onChange={(e) => change(e.target.value)}
          />
          <small>
            {" "}{t("Адрес будет применён устройством после успешной синхронизации.")}{" "}</small>
        </Field>
        {networkEditing && (
          <Field
            label={t("Ключ доступа Bridge")}
            help={t("Ключ не возвращается API. Пустое поле сохраняет действующий ключ.")}
          >
            <TextInput
              type="password"
              autoComplete="new-password"
              value={networkCredentials.bridge_key ?? ""}
              onChange={(e) => {
                const value = e.target.value;
                setNetworkCredentials((old) => ({ ...old, bridge_key: value }));
              }}
            />
          </Field>
        )}
        <div className="bridge-apply-state">
          <StatusPill state={applyStatus.state} label={applyStatus.label} />
        </div>
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Доступ к панели")}</SectionHeading>
        <div className="readout">
          <span>{t("Способ входа")}</span>
          <strong>
            {authMethodLabel(identity?.method, t)}
            {identity?.email ? ` · ${identity.email}` : ""}
          </strong>
        </div>
      </section>
      <div className="info-box">
        <Icon name="info" size={20} />
        <div>
          <strong>{t("Если дисплей не в сети")}</strong>
          <p>
            {" "}{t("Сохранённые сетевые данные применятся при следующем подключении. При ошибке устройство должно восстановить прежнюю сеть.")}{" "}</p>
        </div>
      </div>
    </div>
  );
}
