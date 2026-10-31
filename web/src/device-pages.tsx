import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { formatUptime, type Command } from "./types";
import type { PageProps } from "./page-types";
import { errorMessage } from "./error-messages";
import {
  appliedSettingsStatus,
  canRun,
  Icon,
  SectionHeading,
  Segmented,
  StatusPill,
  Toggle,
  type Ask,
} from "./ui";
import { dateLocale, useLocale, useT } from "./i18n";

const capabilityLabels: Record<string, string> = {
  preferences: "Настройки",
  photo_start: "Запуск фоторамки",
  restart: "Перезапуск",
  ota_check: "Проверка обновлений",
  ota_install: "Установка обновлений",
  reset: "Сброс настроек дисплея",
};

export function UpdatesPage({
  draft,
  edit,
  envelope,
  updates,
  firmwareObservationFresh,
  command,
  pendingCommand,
  ask,
  busy,
}: PageProps) {
  const t = useT();
  const { locale } = useLocale();
  const snapshotsConflict = firmwareObservationFresh && updates?.device_connected === true && envelope.device.connected === true &&
    !!updates?.current_version && !!envelope.device.firmware && !sameFirmwareVersion(updates.current_version, envelope.device.firmware);
  const onlineObservation = firmwareObservationFresh && updates?.device_connected === true && envelope.device.connected === true && !snapshotsConflict;
  const observedVersion = updates?.current_version || envelope.device.firmware || "";
  const currentVersion = observedVersion;
  const available = updates?.available_version || "";
  const installedMatchesAvailable = hasCurrentFirmwareObservation(onlineObservation, currentVersion, available, envelope.device.firmware);
  const latestAttemptInstalled = onlineObservation && !!updates?.history?.[0]?.version && sameFirmwareVersion(currentVersion, updates.history[0].version);
  return (
    <div className="content-narrow">
      <section className="settings-section">
        <SectionHeading>{t("Текущая версия дисплея")}</SectionHeading>
        <div className="version-card">
          <div>
            <small>{t("Текущая версия")}</small>
            <strong className="version-value">
              {currentVersion || t("Не сообщается")}
            </strong>
            {currentVersion && (
              <StatusPill
                state={!onlineObservation ? "muted" : available && updates?.update_available && !installedMatchesAvailable ? "warn" : "good"}
                label={
                  !onlineObservation
                    ? t("Последняя известная версия")
                    : available && updates?.update_available && !installedMatchesAvailable
              ? t("Доступно обновление")
              : t("Версия установлена")
                }
              />
            )}
            {!onlineObservation && currentVersion && <small>{t(envelope.device.connected ? "Показана последняя известная версия; актуальность не подтверждена" : "Показана последняя известная версия; дисплей не в сети")}</small>}
            {snapshotsConflict && <small>{t("Последние ответы Bridge расходятся по версии")}</small>}
            {latestAttemptInstalled && <small>{t("Версия подтверждена текущим ответом дисплея")}</small>}
          </div>
          <div>
            <small>{t("Дисплей")}</small>
            <strong>{draft.screen.name || "HomeDash"}</strong>
          </div>
          <button
            className="button secondary"
            disabled={
              !canRun(envelope.device, "ota_check") || busy || pendingCommand
            }
            onClick={() =>
              ask({
                title: "Проверить обновления?",
                description:
                  "Bridge проверит доступность версии для подключённого дисплея.",
                action: "ota_check",
              })
            }
          >
            <Icon name="refresh" size={18} /> {" "}{t("Проверить обновления")}{" "}</button>
        </div>
        {available && updates?.update_available && !installedMatchesAvailable && !snapshotsConflict && (
          <div className="update-available">
            <span>{t("Доступна версия")}</span>
            <strong>{available}</strong>
            <button
              className="button primary"
              disabled={
                !canRun(envelope.device, "ota_install") ||
                busy ||
                pendingCommand
              }
              onClick={() =>
                ask({
                  title: "Установить обновление?",
                  description: `Устройство установит версию ${available} и может перезагрузиться. Не отключайте питание.`,
                  action: "ota_install",
                })
              }
            >
              {" "}{t("Установить…")}{" "}</button>
          </div>
        )}
        {installedMatchesAvailable && <p className="muted">{t("Доступное обновление уже установлено")}</p>}
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Автоматические действия")}</SectionHeading>
        <div className="preference-row">
          <div>
            <strong>{t("Проверять новые версии")}</strong>
            <small>{t("Фоновая проверка доступности обновлений.")}</small>
          </div>
          <Toggle
            label={t("Автоматически проверять обновления")}
            checked={draft.screen.auto_update_check}
            onChange={(value) =>
              edit({
                ...draft,
                screen: { ...draft.screen, auto_update_check: value },
              })
            }
          />
        </div>
        <div className="preference-row">
          <div>
            <strong>{t("Установка обновлений")}</strong>
            <small>{t("Выберите, спрашивать ли перед установкой.")}</small>
          </div>
          <Segmented
            value={draft.screen.auto_install ? "auto" : "confirm"}
            options={[
              ["confirm", "Спрашивать"],
              ["auto", "Автоматически"],
            ]}
            onChange={(value) =>
              edit({
                ...draft,
                screen: { ...draft.screen, auto_install: value === "auto" },
              })
            }
          />
        </div>
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Состояние команды")}</SectionHeading>
        {command ? (
          <CommandCard command={command} online={onlineObservation} currentVersion={currentVersion} />
        ) : (
          <p className="muted">{t("Нет активных или последних команд.")}</p>
        )}
        {pendingCommand && (
          <small>
            {" "}{t("Ожидание ответа дисплея. Новая команда будет доступна после завершения текущей.")}{" "}</small>
        )}
      </section>
      <section className="settings-section">
        <SectionHeading>{t("История устройства")}</SectionHeading>
        {updates?.history?.length ? (
          <div className="history-list">
            {updates.history.map((item, index) => (
              <div
                className="history-item"
                key={item.id || `${item.version}-${index}`}
              >
                <strong>{item.version || t("Версия неизвестна")}</strong>
                <StatusPill
                  state={
                    item.state === "succeeded"
                      ? "good"
                      : item.state === "failed"
                        ? "bad"
                        : "muted"
                  }
                  label={t(({ queued: "В очереди", accepted: "Принята", succeeded: "Установлена", failed: "Ошибка", unknown: "Подтверждение не получено" } as Record<string, string>)[item.state] ?? "Статус неизвестен")}
                />
                {item.at && <time>{new Date(item.at).toLocaleString(dateLocale(locale))}</time>}
                {item.previous_version && <small>{t("Предыдущая версия")}: {item.previous_version}</small>}
                {item.installed_version && <small>{t("Подтверждённая версия")}: {item.installed_version}</small>}
                {item.stage && <small>{t(({ checking: "Проверка", downloading: "Загрузка", verifying: "Проверка файла", writing: "Запись", rebooting: "Перезагрузка" } as Record<string, string>)[item.stage] ?? "Этап неизвестен")}</small>}
                {item.error && <small>{t(({ ota_check_failed: "Ошибка проверки обновлений", no_update: "Обновление недоступно", download_failed: "Ошибка загрузки", validation_failed: "Ошибка проверки файла", flash_failed: "Ошибка записи", device_error: "Ошибка устройства", unknown: "Причина неизвестна" } as Record<string, string>)[item.error] ?? "Причина неизвестна")}</small>}
              </div>
            ))}
          </div>
        ) : (
          <p className="muted">
            {" "}{t("Попыток обновления пока нет.")}{" "}</p>
        )}
      </section>
      {pendingCommand &&
        envelope.device.ota_progress !== undefined &&
        Number.isFinite(envelope.device.ota_progress) && (
          <section className="settings-section">
            <SectionHeading>{t("Выполнение команды")}</SectionHeading>
            <progress
              max={100}
              value={Math.min(100, Math.max(0, envelope.device.ota_progress))}
            />
            <span>
              {Math.min(100, Math.max(0, envelope.device.ota_progress))}%
            </span>
          </section>
        )}
    </div>
  );
}

export function sameFirmwareVersion(left: string, right: string) {
  const normalize = (value: string) => value.trim().replace(/^[vV]/, "");
  return !!normalize(left) && normalize(left) === normalize(right);
}
export function hasCurrentFirmwareObservation(online: boolean, current: string, target: string, envelopeVersion = "") {
  return online && (!envelopeVersion || !current || sameFirmwareVersion(current, envelopeVersion)) && sameFirmwareVersion(current, target);
}
export function shouldShowCommandError(command: Command, online: boolean, current: string) {
  return !(command.action === "ota_install" && (
    ["interrupted", "expired"].includes(command.state) ||
    hasCurrentFirmwareObservation(online, current, command.target_version || "")
  ));
}
function CommandCard({ command, online = false, currentVersion = "" }: { command: Command; online?: boolean; currentVersion?: string }) {
  const t = useT();
  const { locale } = useLocale();
  const state =
    command.state === "succeeded"
      ? "good"
      : command.state === "failed"
        ? "bad"
        : command.state === "interrupted"
          ? "warn"
          : "muted";
  const stateLabel: Record<string, string> = {
    accepted: "Принята",
    succeeded: "Выполнена",
    failed: "Ошибка",
    expired: "Истекло время",
    cancelled: "Отменена",
    interrupted: "Прервана при перезапуске",
  };
  const actionLabel: Record<string, string> = {
    photo_start: "Запуск фоторамки",
    backlight_adjust: "Подбор яркости",
    backlight_begin: "Подбор яркости",
    backlight_preview: "Проверка яркости",
    backlight_restore: "Восстановить яркость",
    restart: "Перезапуск",
    ota_check: "Проверка обновлений",
    ota_install: "Установка обновления",
    reset: "Сброс настроек дисплея",
  };
  return (
    <div className="command-card">
      <div>
        <strong>{t(actionLabel[command.action] ?? "Команда устройства")}</strong>
        <StatusPill
          state={state}
          label={stateLabel[command.state] ?? "Статус неизвестен"}
        />
      </div>
      <small>{t("Создана")}{" "}{new Date(command.created_at).toLocaleString(dateLocale(locale))}</small>
      {command.error && shouldShowCommandError(command, online, currentVersion) && <p className="error-text">{t(errorMessage(command.error, "Дисплей не смог выполнить команду. Повторите попытку после подключения устройства."))}</p>}
    </div>
  );
}

export function DevicePage({
  envelope,
  firmwareObservationFresh,
  command,
  pendingCommand,
  ask,
  busy,
  downloadDiagnostics,
  refreshStatus,
}: PageProps) {
  const t = useT();
  const { locale } = useLocale();
  const d = envelope.device;
  const settingsStatus = appliedSettingsStatus(envelope, t);
  const facts: Array<[string, ReactNode]> = [
    ["Прошивка", d.firmware || t("Не сообщается")],
    ["Bridge", d.connected ? t("На связи") : t("Не в сети")],
    ["Wi-Fi", d.ssid ? <>{t("Подключён · ")}{d.ssid}</> : t("Не сообщается")],
    ["IP-адрес", d.ip || t("Не сообщается")],
    ["Синхронизация времени", d.time_synced ? t("Выполнена") : t("Не подтверждена")],
    ["Последний ответ", d.last_seen ? new Date(d.last_seen).toLocaleString(dateLocale(locale)) : t("Не сообщается")],
    ["Время работы", d.last_seen ? formatUptime(d.uptime_seconds, locale) : t("Не сообщается")],
    ["Настройки", settingsStatus.label],
  ];
  return (
    <div className="content-narrow">
      <section className="settings-section device-summary">
        <div className="device-illustration">
          <Icon name="monitor" size={56} />
        </div>
        <div>
          <h2>{envelope.settings.screen.name || "HomeDash"}</h2>
          <p>{d.model || t("Модель не сообщается")}</p>
          <small>
            {d.width && d.height
              ? `${d.width} × ${d.height}`
              : t("Разрешение не сообщается")}
          </small>
        </div>
      </section>
      <section className="settings-section device-facts">
        {facts.map(([label, value]) => (
          <div key={label}>
            <span>{t(label)}</span>
            <strong>{value}</strong>
          </div>
        ))}
      </section>
      <section className="settings-section">
        <SectionHeading
          detail={
            d.connected ? (
              <StatusPill state="good" label={t("Подключён")} />
            ) : (
              <StatusPill state="warn" label={t("Не в сети")} />
            )
          }
        >
          {" "}{t("Диагностика")}{" "}</SectionHeading>
        <p>{t("Отчёт очищен от паролей, токенов и содержимого фотографий.")}</p>
        <div className="button-row">
          <button
            className="button primary"
            disabled={busy}
            onClick={() => downloadDiagnostics()}
          >
            <Icon name="download" size={18} /> {" "}{t("Скачать диагностику")}{" "}</button>
          <button
            className="button secondary"
            disabled={busy}
            onClick={() => refreshStatus()}
          >
            <Icon name="refresh" size={18} /> {" "}{t("Обновить статус")}{" "}</button>
        </div>
        {d.error && <div className="alert error">{t(errorMessage(d.error, "Не удалось выполнить действие. Проверьте введённые данные и повторите попытку."))}</div>}
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Действия с устройством")}</SectionHeading>
        <div className="device-actions">
          <button
            className="button secondary"
            disabled={!canRun(d, "restart") || busy || pendingCommand}
            onClick={() =>
              ask({
                title: "Перезапустить дисплей?",
                description:
                  "Экран временно отключится. Сохранённые настройки останутся.",
                action: "restart",
              })
            }
          >
            <Icon name="restart" size={18} /> {" "}{t("Перезапустить дисплей")}{" "}</button>
          <div className="danger-zone">
            <h3>{t("Сброс настроек дисплея")}</h3>
            <p>
              {" "}{t("Устройство удалит параметры Wi-Fi, адрес Bridge и локальные настройки дисплея. Настройки панели и сервисов на сервере сохранятся.")}{" "}</p>
            <button
              className="button danger-outline"
              disabled={!canRun(d, "reset") || busy || pendingCommand}
              onClick={() =>
                ask({
                  title: "Сбросить настройки дисплея?",
                  description:
                    "Устройство удалит сохранённые параметры Wi-Fi, адрес Bridge и локальные настройки дисплея. Настройки панели и сервисов на сервере сохранятся. Для восстановления подключения потребуется локальный доступ к устройству.",
                  action: "reset",
                  danger: true,
                })
              }
            >
              <Icon name="trash" size={18} /> {" "}{t("Сбросить настройки дисплея…")}{" "}</button>
          </div>
        </div>
        {pendingCommand && command && <CommandCard command={command} online={firmwareObservationFresh && d.connected} currentVersion={d.firmware} />}
      </section>
      <section className="settings-section">
        <SectionHeading>{t("Возможности устройства")}</SectionHeading>
        {d.capabilities.some((item) => capabilityLabels[item]) ? (
          <div className="capability-list">
            {d.capabilities
              .filter((item) => capabilityLabels[item])
              .map((item) => (
                <span key={item} className="badge">
                  {t(capabilityLabels[item])}
                </span>
              ))}
          </div>
        ) : (
          <p className="muted">{t("Нет сведений о возможностях устройства.")}</p>
        )}
      </section>
    </div>
  );
}

export function ConfirmDialog({
  ask,
  busy,
  onClose,
  onConfirm,
}: {
  ask: Ask;
  busy: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const t = useT();
  const confirmRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLElement>(null);
  useEffect(() => {
    confirmRef.current?.focus();
    const trap = (event: KeyboardEvent) => {
      if (event.key !== "Tab" || !dialogRef.current) return;
      const controls = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(
          'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
        ),
      );
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    document.addEventListener("keydown", trap);
    return () => document.removeEventListener("keydown", trap);
  }, []);
  return (
    <div
      className="modal-backdrop"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <section
        ref={dialogRef}
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-description"
      >
        <button className="dialog-close" aria-label={t("Закрыть")} onClick={onClose}>
          <Icon name="close" size={19} />
        </button>
        <div className={`dialog-icon ${ask.danger ? "danger" : ""}`}>
          <Icon name={ask.danger ? "trash" : "restart"} size={22} />
        </div>
        <h2 id="confirm-title">{t(ask.title)}</h2>
        <p id="confirm-description">{t(ask.description)}</p>
        <div className="button-row">
          <button
            className="button secondary"
            onClick={onClose}
            disabled={busy}
          >
            {" "}{t("Отмена")}{" "}</button>
          <button
            ref={confirmRef}
            className={`button ${ask.danger ? "danger" : "primary"}`}
            disabled={busy}
            onClick={onConfirm}
          >
            {busy
              ? t("Отправка…")
              : ask.action === "ota_install"
                ? t("Установить")
                : ask.action === "reset"
                  ? t("Сбросить")
                  : t("Подтвердить")}
          </button>
        </div>
      </section>
    </div>
  );
}
