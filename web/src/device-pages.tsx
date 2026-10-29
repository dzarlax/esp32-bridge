import { useEffect, useRef } from "react";
import { formatUptime, type Command } from "./types";
import type { PageProps } from "./page-types";
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
  command,
  pendingCommand,
  ask,
  busy,
}: PageProps) {
  const currentVersion =
    updates?.current_version || envelope.device.firmware || "";
  const available = updates?.available_version || "";
  return (
    <div className="content-narrow">
      <section className="settings-section">
        <SectionHeading>Установленная версия</SectionHeading>
        <div className="version-card">
          <div>
            <small>Текущая версия</small>
            <strong className="version-value">
              {currentVersion || "Не сообщается"}
            </strong>
            {currentVersion && (
              <StatusPill
                state={available && updates?.update_available ? "warn" : "good"}
                label={
                  available && updates?.update_available
                    ? "Доступно обновление"
                    : "Версия установлена"
                }
              />
            )}
          </div>
          <div>
            <small>Дисплей</small>
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
            <Icon name="refresh" size={18} /> Проверить обновления
          </button>
        </div>
        {available && updates?.update_available && (
          <div className="update-available">
            <span>Доступна версия</span>
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
              Установить…
            </button>
          </div>
        )}
      </section>
      <section className="settings-section">
        <SectionHeading>Автоматические действия</SectionHeading>
        <div className="preference-row">
          <div>
            <strong>Проверять новые версии</strong>
            <small>Фоновая проверка доступности обновлений.</small>
          </div>
          <Toggle
            label="Автоматически проверять обновления"
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
            <strong>Установка обновлений</strong>
            <small>Выберите, спрашивать ли перед установкой.</small>
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
        <SectionHeading>Состояние команды</SectionHeading>
        {command ? (
          <CommandCard command={command} />
        ) : (
          <p className="muted">Нет активных или последних команд.</p>
        )}
        {pendingCommand && (
          <small>
            Ожидание ответа дисплея. Новая команда будет доступна после
            завершения текущей.
          </small>
        )}
      </section>
      <section className="settings-section">
        <SectionHeading>История устройства</SectionHeading>
        {updates?.history?.length ? (
          <div className="history-list">
            {updates.history.map((item, index) => (
              <div
                className="history-item"
                key={`${item.version}-${item.at ?? index}`}
              >
                <strong>{item.version}</strong>
                <StatusPill
                  state={
                    item.state === "succeeded" || item.state === "installed"
                      ? "good"
                      : item.state === "failed"
                        ? "bad"
                        : "muted"
                  }
                  label={item.state}
                />
                {item.at && <time>{new Date(item.at).toLocaleString()}</time>}
              </div>
            ))}
          </div>
        ) : (
          <p className="muted">
            История обновлений пока не сообщается устройством.
          </p>
        )}
      </section>
      {pendingCommand &&
        envelope.device.ota_progress !== undefined &&
        Number.isFinite(envelope.device.ota_progress) && (
          <section className="settings-section">
            <SectionHeading>Выполнение команды</SectionHeading>
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
function CommandCard({ command }: { command: Command }) {
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
    restart: "Перезапуск",
    ota_check: "Проверка обновлений",
    ota_install: "Установка обновления",
    reset: "Сброс настроек дисплея",
  };
  return (
    <div className="command-card">
      <div>
        <strong>{actionLabel[command.action] ?? "Команда устройства"}</strong>
        <StatusPill
          state={state}
          label={stateLabel[command.state] ?? "Статус неизвестен"}
        />
      </div>
      <small>Создана {new Date(command.created_at).toLocaleString()}</small>
      {command.error && <p className="error-text">{command.error}</p>}
    </div>
  );
}

export function DevicePage({
  envelope,
  command,
  pendingCommand,
  ask,
  busy,
  downloadDiagnostics,
  refreshStatus,
}: PageProps) {
  const d = envelope.device;
  const settingsStatus = appliedSettingsStatus(d, envelope.revision);
  return (
    <div className="content-narrow">
      <section className="settings-section device-summary">
        <div className="device-illustration">
          <Icon name="monitor" size={56} />
        </div>
        <div>
          <h2>{envelope.settings.screen.name || "HomeDash"}</h2>
          <p>{d.model || "Модель не сообщается"}</p>
          <small>
            {d.width && d.height
              ? `${d.width} × ${d.height}`
              : "Разрешение не сообщается"}
          </small>
        </div>
      </section>
      <section className="settings-section device-facts">
        {[
          ["Прошивка", d.firmware || "Не сообщается"],
          ["Bridge", d.connected ? "На связи" : "Не в сети"],
          ["Wi-Fi", d.ssid ? `Подключён · ${d.ssid}` : "Не сообщается"],
          ["IP-адрес", d.ip || "Не сообщается"],
          [
            "Синхронизация времени",
            d.time_synced ? "Выполнена" : "Не подтверждена",
          ],
          [
            "Последний ответ",
            d.last_seen
              ? new Date(d.last_seen).toLocaleString()
              : "Не сообщается",
          ],
          [
            "Время работы",
            d.last_seen ? formatUptime(d.uptime_seconds) : "Не сообщается",
          ],
          ["Настройки", settingsStatus.label],
        ].map(([label, value]) => (
          <div key={label}>
            <span>{label}</span>
            <strong>{value}</strong>
          </div>
        ))}
      </section>
      <section className="settings-section">
        <SectionHeading
          detail={
            d.connected ? (
              <StatusPill state="good" label="Подключён" />
            ) : (
              <StatusPill state="warn" label="Не в сети" />
            )
          }
        >
          Диагностика
        </SectionHeading>
        <p>Отчёт очищен от паролей, токенов и содержимого фотографий.</p>
        <div className="button-row">
          <button
            className="button primary"
            disabled={busy}
            onClick={() => downloadDiagnostics()}
          >
            <Icon name="download" size={18} /> Скачать диагностику
          </button>
          <button
            className="button secondary"
            disabled={busy}
            onClick={() => refreshStatus()}
          >
            <Icon name="refresh" size={18} /> Обновить статус
          </button>
        </div>
        {d.error && <div className="alert error">{d.error}</div>}
      </section>
      <section className="settings-section">
        <SectionHeading>Действия с устройством</SectionHeading>
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
            <Icon name="restart" size={18} /> Перезапустить дисплей
          </button>
          <div className="danger-zone">
            <h3>Сброс настроек дисплея</h3>
            <p>
              Устройство удалит параметры Wi-Fi, адрес Bridge и локальные
              настройки дисплея. Настройки панели и сервисов на сервере
              сохранятся.
            </p>
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
              <Icon name="trash" size={18} /> Сбросить настройки дисплея…
            </button>
          </div>
        </div>
        {pendingCommand && command && <CommandCard command={command} />}
      </section>
      <section className="settings-section">
        <SectionHeading>Возможности устройства</SectionHeading>
        {d.capabilities.some((item) => capabilityLabels[item]) ? (
          <div className="capability-list">
            {d.capabilities
              .filter((item) => capabilityLabels[item])
              .map((item) => (
                <span key={item} className="badge">
                  {capabilityLabels[item]}
                </span>
              ))}
          </div>
        ) : (
          <p className="muted">Нет сведений о возможностях устройства.</p>
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
        <button className="dialog-close" aria-label="Закрыть" onClick={onClose}>
          <Icon name="close" size={19} />
        </button>
        <div className={`dialog-icon ${ask.danger ? "danger" : ""}`}>
          <Icon name={ask.danger ? "trash" : "restart"} size={22} />
        </div>
        <h2 id="confirm-title">{ask.title}</h2>
        <p id="confirm-description">{ask.description}</p>
        <div className="button-row">
          <button
            className="button secondary"
            onClick={onClose}
            disabled={busy}
          >
            Отмена
          </button>
          <button
            ref={confirmRef}
            className={`button ${ask.danger ? "danger" : "primary"}`}
            disabled={busy}
            onClick={onConfirm}
          >
            {busy
              ? "Отправка…"
              : ask.action === "ota_install"
                ? "Установить"
                : ask.action === "reset"
                  ? "Сбросить"
                  : "Подтвердить"}
          </button>
        </div>
      </section>
    </div>
  );
}
