import previewPhoto from "./assets/photo-preview.webp";
import { useId } from "react";
import { numberError, photoTimingLimits } from "./validation";
import { type Settings } from "./types";
import type { PageProps } from "./page-types";
import {
  canRun,
  Field,
  Icon,
  SectionHeading,
  Segmented,
  Select,
  StatusPill,
  TextInput,
  Toggle,
  tzLabel,
} from "./ui";
import { useT } from "./i18n";

export function ScreenPage({ draft, edit, envelope }: PageProps) {
  const t = useT();
  const idleHelp = useId();
  const s = draft.screen;
  const set = (key: keyof Settings["screen"], value: string | boolean) =>
    edit({ ...draft, screen: { ...s, [key]: value } });
  return (
    <>
      <section className="settings-section">
        <SectionHeading>{t("Устройство")}</SectionHeading>
        <Field label={t("Имя дисплея")}>
          <TextInput
            value={s.name}
            maxLength={80}
            onChange={(e) => set("name", e.target.value)}
          />
        </Field>
        <Field label={t("Язык дисплея")} help={!envelope.device.capabilities.includes("locale") ? t("Язык дисплея не поддерживается этой прошивкой.") : undefined}>
          <Select
            value={s.locale ?? "ru"}
            disabled={!envelope.device.capabilities.includes("locale")}
            onChange={(e) => set("locale", e.target.value)}
          >
            {(envelope.capabilities.locales ?? ["ru", "en", "sr-Latn"]).map((locale) => (
              <option key={locale} value={locale}>{locale === "en" ? "English" : locale === "sr-Latn" ? "Srpski (latinica)" : t("Русский")}</option>
            ))}
          </Select>
        </Field>
        {envelope.device.applied_locale && <p className="field-note">{t("Применённый язык")}: {envelope.device.applied_locale === "en" ? "English" : envelope.device.applied_locale === "sr-Latn" ? "Srpski (latinica)" : t("Русский")}</p>}
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Время и место")}</SectionHeading>
        <Field label={t("Подпись города")} help={t("Это подпись на экране, а не поиск города. Координаты погоды задаются в разделе «Данные и сервисы».")}>
          <TextInput
            value={s.city}
            maxLength={100}
            onChange={(e) => set("city", e.target.value)}
          />
        </Field>
        <Field label={t("Часовой пояс")}>
          <Select
            value={s.timezone}
            onChange={(e) => set("timezone", e.target.value)}
          >
            {envelope.capabilities.timezones.map((tz) => (
              <option key={tz} value={tz}>
                {tzLabel(tz, t)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Формат времени")}>
          <Segmented
            value={s.clock_format}
            options={[
              ["24h", t("24 часа")],
              ["12h", t("12 часов")],
            ]}
            onChange={(value) => set("clock_format", value)}
          />
        </Field>
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Режим покоя")}</SectionHeading>
        <Field label={t("При бездействии")} help={<NumberHelp id={idleHelp} value={draft.photos.idle_seconds} {...photoTimingLimits.idle_seconds} />}>
          <span className="inline-row">
            <BoundedNumberInput
              label={t("Задержка бездействия")}
              helpId={idleHelp}
              timing="idle_seconds"
              value={draft.photos.idle_seconds}
              min={photoTimingLimits.idle_seconds.min}
              max={photoTimingLimits.idle_seconds.max}
              unit={t("сек")}
              onChange={(value) =>
                edit({
                  ...draft,
                  photos: { ...draft.photos, idle_seconds: value },
                })
              }
            />
            <span className="inline-label">{t("затем")}</span>
            <Select
              aria-label={t("Действие после бездействия")}
              value={s.idle_action}
              onChange={(e) => set("idle_action", e.target.value)}
            >
              <option value="photos">{t("Фоторамка")}</option>
              <option value="off">{t("Выключить экран")}</option>
            </Select>
          </span>
        </Field>
        <Field label={t("Ночной режим")}>
          <div className="night-row">
            <Toggle
              label={
                s.night_enabled
                  ? t("Ночной режим включён")
                  : t("Ночной режим выключен")
              }
              checked={s.night_enabled}
              onChange={(value) => set("night_enabled", value)}
            />
            <TextInput
              type="time"
              aria-label={t("Начало ночного режима")}
              value={s.night_start}
              onChange={(e) => set("night_start", e.target.value)}
              disabled={!s.night_enabled}
            />
            <span>—</span>
            <TextInput
              type="time"
              aria-label={t("Конец ночного режима")}
              value={s.night_end}
              onChange={(e) => set("night_end", e.target.value)}
              disabled={!s.night_enabled}
            />
          </div>
          <small>
            {" "}{t("Касание разбудит экран. Яркость недоступна: устройство не сообщает эту возможность.")}{" "}</small>
        </Field>
      </section>
      <section className="settings-section">
        <SectionHeading
          detail={
            <StatusPill
              state={envelope.capabilities.brightness ? "good" : "muted"}
              label={
                envelope.capabilities.brightness
                  ? t("Поддерживается")
                  : t("Не поддерживается")
              }
            />
          }
        >
          {" "}{t("Яркость")}{" "}</SectionHeading>
        <Field label={t("Управление яркостью")}>
          <TextInput
            disabled
            value={t("Недоступно для этого устройства")}
            aria-label={t("Яркость не поддерживается")}
          />
        </Field>
      </section>
    </>
  );
}

export function PhotosPage({
  draft,
  edit,
  envelope,
  onOpenConnections,
  runAction,
  busy,
}: PageProps) {
  const t = useT();
  const idleHelp = useId();
  const intervalHelp = useId();
  const p = draft.photos;
  const canStartPhotoFrame =
    canRun(envelope.device, "photo_start") && p.enabled && p.source !== "none";
  const set = (
    key: keyof Settings["photos"],
    value: string | number | boolean,
  ) => edit({ ...draft, photos: { ...p, [key]: value } });
  return (
    <>
      <section className="settings-section">
        <SectionHeading
          detail={
            <Toggle
              label={
                p.enabled ? t("Фоторамка включена") : t("Фоторамка выключена")
              }
              checked={p.enabled}
              onChange={(value) => set("enabled", value)}
            />
          }
        >
          {" "}{t("Источник фотографий")}{" "}</SectionHeading>
        <div className="photos-source-preview">
          <div className="photos-source-column">
            <div className="source-options">
              <label
                className={`source-option ${p.source === "immich" ? "chosen" : ""}`}
              >
                <input
                  type="radio"
                  name="photo-source"
                  value="immich"
                  checked={p.source === "immich"}
                  onChange={() => set("source", "immich")}
                />
                <span className="source-symbol source-icon">
                  <Icon name="image" size={22} />
                </span>
                <span>
                  <strong>Immich</strong>
                  <small>
                    {envelope.secrets_present.immich_api_key
                      ? t("Ключ сохранён")
                      : t("Настройте адрес и ключ доступа")}
                  </small>
                </span>
              </label>
              <label className="source-option unavailable">
                <input
                  type="radio"
                  disabled
                  checked={false}
                  name="photo-source"
                />
                <span className="source-wordmark" aria-hidden="true">
                  Google
                </span>
                <span>
                  <strong>Google Photos</strong>
                  <small>{t("Интеграция недоступна")}</small>
                </span>
                <span className="badge">{t("Недоступно")}</span>
              </label>
              <label
                className={`source-option ${p.source === "none" ? "chosen" : ""}`}
              >
                <input
                  type="radio"
                  name="photo-source"
                  value="none"
                  checked={p.source === "none"}
                  onChange={() => set("source", "none")}
                />
                <span className="source-symbol source-icon">
                  <Icon name="close" size={20} />
                </span>
                <span>
                  <strong>{t("Без источника")}</strong>
                  <small>{t("Фотографии не загружаются")}</small>
                </span>
              </label>
            </div>
            {p.source === "fixture" && (
              <div className="source-option chosen unavailable current-fixture">
                <input
                  type="radio"
                  checked
                  disabled
                  aria-label={t("Текущий fixture источник")}
                />
                <span className="source-symbol source-icon">
                  <Icon name="image" size={22} />
                </span>
                <span>
                  <strong>{t("Fixture — текущая настройка")}</strong>
                  <small>
                    {" "}{t("Доступно только в существующих fixture-развёртываниях.")}{" "}</small>
                </span>
              </div>
            )}
            {p.source === "immich" && (
              <div className="nested-form">
                <Field label={t("Общий сервер Immich")} help={t("Адрес и ключ Immich общие для всех дисплеев и редактируются отдельно.")}>
                  <div className="shared-connection-field">
                    <TextInput value={p.base_url || t("Не настроено")} readOnly aria-label={t("Общий сервер Immich, только чтение")} />
                    <button className="button secondary" type="button" onClick={onOpenConnections}>{t("Общие подключения")}</button>
                  </div>
                </Field>
                <Field
                  label="Album ID"
                  help={t("Введите UUID альбома из Immich. Выбор из списка пока не поддерживается панелью.")}
                >
                  <TextInput
                    value={p.album_id}
                    placeholder={t("UUID альбома")}
                    onChange={(e) => set("album_id", e.target.value)}
                  />
                </Field>
                <small>{envelope.secrets_present.immich_api_key ? t("Ключ Immich сохранён для общих подключений.") : t("Ключ Immich ещё не настроен.")}</small>
              </div>
            )}
          </div>
          <div className="preview-wrap">
            <div>
              <strong>{t("Предпросмотр размещения")}</strong>
              <small>
                {" "}{t("Пример размещения на иллюстрации. Фотографии из вашего источника здесь не загружаются.")}{" "}</small>
            </div>
            <div
              className={`photo-preview ${p.layout}`}
              role="img"
              aria-label={t("Пример размещения вертикального фото")}
            >
              <img
                className="photo-preview-background"
                src={previewPhoto}
                alt=""
                aria-hidden="true"
              />
              <img
                className="photo-preview-image"
                src={previewPhoto}
                alt={t("Иллюстрация горного водопада")}
              />
              <div className={`photo-preview-overlays${envelope.device.capabilities.includes("photo_overlay_adaptive") ? " adaptive" : ""}`} aria-hidden="true">
                <span className="preview-demo">{t("Демо")}</span>
                {(p.show_clock ?? true) && (
                  <div className="preview-clock">
                    <strong>18:42</strong>
                    <span>{t("Пятница, 30 октября")}</span>
                  </div>
                )}
                {(p.show_weather ?? true) && (
                  <div className="preview-weather">
                    <svg viewBox="0 0 32 32" focusable="false">
                      <circle cx="13" cy="13" r="5" />
                      <path d="M13 2v3m0 16v3M2 13h3m16 0h3M5.2 5.2l2.1 2.1m11.4 11.4 2.1 2.1m0-15.6-2.1 2.1M7.3 18.7l-2.1 2.1" />
                      <path d="M13 18h10a4 4 0 0 1 0 8h-9a4 4 0 0 1-1-8Z" />
                    </svg>
                    <span><strong>18°</strong><small>{t("Ясно")}</small></span>
                  </div>
                )}
                {(p.show_event ?? true) && (
                  <div className="preview-event">
                    <span>{t("СЛЕДУЮЩЕЕ СОБЫТИЕ")}</span>
                    <strong>{t("Встреча · 19:30")}</strong>
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Показ")}</SectionHeading>
        <Field
          label={t("Информация поверх фото")}
          help={t("Погода и события используют настроенные на дисплее источники. Даты и подписи форматируются на языке дисплея.")}
        >
          <div className="photo-overlay-toggles">
            <Toggle
              label={t("Часы и дата")}
              checked={p.show_clock ?? true}
              onChange={(value) => set("show_clock", value)}
            />
            <Toggle
              label={t("Погода")}
              checked={p.show_weather ?? true}
              onChange={(value) => set("show_weather", value)}
            />
            <Toggle
              label={t("Ближайшее событие")}
              checked={p.show_event ?? true}
              onChange={(value) => set("show_event", value)}
            />
          </div>
          {!envelope.device.capabilities.includes("photo_overlay") ? (
            <small className="field-note">
              {t("Информационный слой появится после обновления прошивки дисплея.")}
            </small>
          ) : !envelope.device.capabilities.includes("photo_overlay_adaptive") ? (
            <small className="field-note">
              {t("Адаптивное оформление появится после обновления прошивки дисплея.")}
            </small>
          ) : null}
        </Field>
        <Field label={t("Запуск после бездействия")} help={<NumberHelp id={idleHelp} value={p.idle_seconds} {...photoTimingLimits.idle_seconds} />}>
          <BoundedNumberInput
            label={t("Запуск после бездействия")}
            helpId={idleHelp}
            timing="idle_seconds"
            value={p.idle_seconds}
            min={photoTimingLimits.idle_seconds.min}
            max={photoTimingLimits.idle_seconds.max}
            unit={t("сек")}
            onChange={(value) => set("idle_seconds", value)}
          />
        </Field>
        <Field label={t("Смена фотографии")} help={<NumberHelp id={intervalHelp} value={p.interval_seconds} {...photoTimingLimits.interval_seconds} />}>
          <BoundedNumberInput
            label={t("Интервал смены фотографии")}
            helpId={intervalHelp}
            timing="interval_seconds"
            value={p.interval_seconds}
            min={photoTimingLimits.interval_seconds.min}
            max={photoTimingLimits.interval_seconds.max}
            unit={t("сек")}
            onChange={(value) => set("interval_seconds", value)}
          />
        </Field>
        <Field label={t("Порядок")}>
          <Select
            value={p.order}
            onChange={(e) => set("order", e.target.value)}
          >
            <option value="shuffle">{t("Случайный")}</option>
            <option value="sequential">{t("По порядку")}</option>
          </Select>
        </Field>
        <Field label={t("Размещение")}>
          <Select
            value={p.layout}
            onChange={(e) => set("layout", e.target.value)}
          >
            <option value="adaptive">{t("Адаптивное")}</option>
            <option value="contain">{t("Целиком")}</option>
            <option value="cover">{t("Заполнить экран")}</option>
          </Select>
        </Field>
        <Field
          label={t("Максимальная обрезка")}
          help={t("Если обрезка превысит порог, фото показывается целиком на размытой подложке.")}
        >
          <span className="range-row">
            <input
              type="range"
              min={0}
              max={40}
              step={1}
              value={p.crop_limit_percent}
              onChange={(e) =>
                set("crop_limit_percent", Number(e.target.value))
              }
            />
            <output>{p.crop_limit_percent}%</output>
          </span>
        </Field>
      </section>
      <div className="page-actions">
        <button
          className="button secondary"
          disabled={!canStartPhotoFrame || busy}
          onClick={() => runAction("photo_start")}
        >
          <Icon name="image" size={18} /> {" "}{t("Запустить на дисплее")}{" "}</button>
        {!canStartPhotoFrame && (
          <small>
            {!p.enabled || p.source === "none"
              ? t("Включите фоторамку и выберите источник, чтобы запустить её на дисплее.")
              : t("Команда недоступна: дисплей не в сети или не сообщил о поддержке этой команды.")}
          </small>
        )}
      </div>
    </>
  );
}

function BoundedNumberInput({
  label,
  helpId,
  timing,
  value,
  min,
  max,
  unit,
  onChange,
}: {
  label: string;
  helpId: string;
  timing: keyof typeof photoTimingLimits;
  value: number;
  min: number;
  max: number;
  unit: string;
  onChange: (v: number) => void;
}) {
  return (
    <span className="number-input">
      <TextInput
        aria-label={label}
        aria-describedby={helpId}
        aria-invalid={Boolean(numberError(value, min, max, unit))}
        data-photo-timing={timing}
        type="number"
        min={min}
        max={max}
        step={1}
        value={Number.isFinite(value) ? value : ""}
        onChange={(e) => onChange(e.target.valueAsNumber)}
      />
      <span>{unit}</span>
    </span>
  );
}

function NumberHelp({ id, value, min, max }: { id: string; value: number; min: number; max: number }) {
  const t = useT();
  const error = numberError(value, min, max, "сек");
  return <span id={id} className={error ? "error-text" : undefined}>{t(error ?? `От ${min} до ${max} сек.`)}</span>;
}
