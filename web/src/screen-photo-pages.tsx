import previewPhoto from "./assets/photo-preview.webp";
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

export function ScreenPage({ draft, edit, envelope }: PageProps) {
  const s = draft.screen;
  const set = (key: keyof Settings["screen"], value: string | boolean) =>
    edit({ ...draft, screen: { ...s, [key]: value } });
  return (
    <>
      <section className="settings-section">
        <SectionHeading>Устройство</SectionHeading>
        <Field label="Имя дисплея">
          <TextInput
            value={s.name}
            maxLength={80}
            onChange={(e) => set("name", e.target.value)}
          />
        </Field>
      </section>
      <section className="settings-section">
        <SectionHeading>Время и место</SectionHeading>
        <Field label="Подпись города" help="Это подпись на экране, а не поиск города. Координаты погоды задаются в разделе «Данные и сервисы».">
          <TextInput
            value={s.city}
            maxLength={100}
            onChange={(e) => set("city", e.target.value)}
          />
        </Field>
        <Field label="Часовой пояс">
          <Select
            value={s.timezone}
            onChange={(e) => set("timezone", e.target.value)}
          >
            {envelope.capabilities.timezones.map((tz) => (
              <option key={tz} value={tz}>
                {tzLabel(tz)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Формат времени">
          <Segmented
            value={s.clock_format}
            options={[
              ["24h", "24 часа"],
              ["12h", "12 часов"],
            ]}
            onChange={(value) => set("clock_format", value)}
          />
        </Field>
      </section>
      <section className="settings-section">
        <SectionHeading>Режим покоя</SectionHeading>
        <Field label="При бездействии">
          <span className="inline-row">
            <BoundedNumberInput
              label="Задержка бездействия"
              value={draft.photos.idle_seconds}
              min={30}
              max={3600}
              unit="сек"
              onChange={(value) =>
                edit({
                  ...draft,
                  photos: { ...draft.photos, idle_seconds: value },
                })
              }
            />
            <span className="inline-label">затем</span>
            <Select
              aria-label="Действие после бездействия"
              value={s.idle_action}
              onChange={(e) => set("idle_action", e.target.value)}
            >
              <option value="photos">Фоторамка</option>
              <option value="off">Выключить экран</option>
            </Select>
          </span>
        </Field>
        <Field label="Ночной режим">
          <div className="night-row">
            <Toggle
              label={
                s.night_enabled
                  ? "Ночной режим включён"
                  : "Ночной режим выключен"
              }
              checked={s.night_enabled}
              onChange={(value) => set("night_enabled", value)}
            />
            <TextInput
              type="time"
              aria-label="Начало ночного режима"
              value={s.night_start}
              onChange={(e) => set("night_start", e.target.value)}
              disabled={!s.night_enabled}
            />
            <span>—</span>
            <TextInput
              type="time"
              aria-label="Конец ночного режима"
              value={s.night_end}
              onChange={(e) => set("night_end", e.target.value)}
              disabled={!s.night_enabled}
            />
          </div>
          <small>
            Касание разбудит экран. Яркость недоступна: устройство не сообщает
            эту возможность.
          </small>
        </Field>
      </section>
      <section className="settings-section">
        <SectionHeading
          detail={
            <StatusPill
              state={envelope.capabilities.brightness ? "good" : "muted"}
              label={
                envelope.capabilities.brightness
                  ? "Поддерживается"
                  : "Не поддерживается"
              }
            />
          }
        >
          Яркость
        </SectionHeading>
        <Field label="Управление яркостью">
          <TextInput
            disabled
            value="Недоступно для этого устройства"
            aria-label="Яркость не поддерживается"
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
                p.enabled ? "Фоторамка включена" : "Фоторамка выключена"
              }
              checked={p.enabled}
              onChange={(value) => set("enabled", value)}
            />
          }
        >
          Источник фотографий
        </SectionHeading>
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
                      ? "Ключ сохранён"
                      : "Настройте адрес и ключ доступа"}
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
                  <small>Интеграция недоступна</small>
                </span>
                <span className="badge">Недоступно</span>
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
                  <strong>Без источника</strong>
                  <small>Фотографии не загружаются</small>
                </span>
              </label>
            </div>
            {p.source === "fixture" && (
              <div className="source-option chosen unavailable current-fixture">
                <input
                  type="radio"
                  checked
                  disabled
                  aria-label="Текущий fixture источник"
                />
                <span className="source-symbol source-icon">
                  <Icon name="image" size={22} />
                </span>
                <span>
                  <strong>Fixture — текущая настройка</strong>
                  <small>
                    Доступно только в существующих fixture-развёртываниях.
                  </small>
                </span>
              </div>
            )}
            {p.source === "immich" && (
              <div className="nested-form">
                <Field label="Общий сервер Immich" help="Адрес и ключ Immich общие для всех дисплеев и редактируются отдельно.">
                  <div className="shared-connection-field">
                    <TextInput value={p.base_url || "Не настроено"} readOnly aria-label="Общий сервер Immich, только чтение" />
                    <button className="button secondary" type="button" onClick={onOpenConnections}>Общие подключения</button>
                  </div>
                </Field>
                <Field
                  label="Album ID"
                  help="Введите UUID альбома из Immich. Выбор из списка пока не поддерживается панелью."
                >
                  <TextInput
                    value={p.album_id}
                    placeholder="UUID альбома"
                    onChange={(e) => set("album_id", e.target.value)}
                  />
                </Field>
                <small>{envelope.secrets_present.immich_api_key ? "Ключ Immich сохранён для общих подключений." : "Ключ Immich ещё не настроен."}</small>
              </div>
            )}
          </div>
          <div className="preview-wrap">
            <div>
              <strong>Предпросмотр размещения</strong>
              <small>
                Пример размещения на иллюстрации. Фотографии из вашего источника здесь не загружаются.
              </small>
            </div>
            <div
              className={`photo-preview ${p.layout}`}
              role="img"
              aria-label="Пример размещения вертикального фото"
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
                alt="Иллюстрация горного водопада"
              />
            </div>
          </div>
        </div>
      </section>
      <section className="settings-section">
        <SectionHeading>Показ</SectionHeading>
        <Field label="Запуск после бездействия">
          <BoundedNumberInput
            label="Запуск после бездействия"
            value={p.idle_seconds}
            min={30}
            max={3600}
            unit="сек"
            onChange={(value) => set("idle_seconds", value)}
          />
        </Field>
        <Field label="Смена фотографии">
          <BoundedNumberInput
            label="Интервал смены фотографии"
            value={p.interval_seconds}
            min={15}
            max={3600}
            unit="сек"
            onChange={(value) => set("interval_seconds", value)}
          />
        </Field>
        <Field label="Порядок">
          <Select
            value={p.order}
            onChange={(e) => set("order", e.target.value)}
          >
            <option value="shuffle">Случайный</option>
            <option value="sequential">По порядку</option>
          </Select>
        </Field>
        <Field label="Размещение">
          <Select
            value={p.layout}
            onChange={(e) => set("layout", e.target.value)}
          >
            <option value="adaptive">Адаптивное</option>
            <option value="contain">Целиком</option>
            <option value="cover">Заполнить экран</option>
          </Select>
        </Field>
        <Field
          label="Максимальная обрезка"
          help="Если обрезка превысит порог, фото показывается целиком на размытой подложке."
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
          <Icon name="image" size={18} /> Запустить на дисплее
        </button>
        {!canStartPhotoFrame && (
          <small>
            {!p.enabled || p.source === "none"
              ? "Включите фоторамку и выберите источник, чтобы запустить её на дисплее."
              : "Команда недоступна: дисплей не в сети или не сообщил о поддержке этой команды."}
          </small>
        )}
      </div>
    </>
  );
}

function BoundedNumberInput({
  label,
  value,
  min,
  max,
  unit,
  onChange,
}: {
  label: string;
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
        type="number"
        min={min}
        max={max}
        step={1}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
      <span>{unit}</span>
    </span>
  );
}
