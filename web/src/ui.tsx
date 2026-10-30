import React from "react";
import type { Device, Envelope, Status } from "./types";
import { useT } from "./i18n";

export type Ask = {
  title: string;
  description: string;
  action: "photo_start" | "restart" | "ota_check" | "ota_install" | "reset";
  danger?: boolean;
};
export const tzLabel = (tz: string, t: (message: string) => string) => (tz === "Europe/Belgrade" ? t("Белград") : tz);
export const authMethodLabel = (method: string | undefined, t: (message: string) => string) =>
  method === "password"
    ? t("Пароль администратора")
    : method === "cloudflare" || method === "cloudflare_access"
      ? "Cloudflare Access"
      : method
        ? t("Другой способ входа")
        : t("Не сообщается");
const statusLabels: Record<Status["state"], string> = {
  disabled: "Отключён",
  unconfigured: "Требуется подключение",
  ready: "Готов",
  error: "Ошибка",
  unknown: "Статус неизвестен",
};
export const canRun = (device: Envelope["device"], action: string) =>
  device.connected && device.capabilities.includes(action);

export function appliedSettingsStatus(device: Device, desiredRevision: number, t: (message: string) => string) {
  if (!device.last_seen) {
    return device.connected
      ? { state: "muted" as const, label: t("Подтверждение не получено") }
      : { state: "warn" as const, label: t("Ожидает подключения") };
  }
  if (device.applied_revision === desiredRevision) {
    return {
      state: "good" as const,
      label: t("Настройки подтверждены устройством"),
    };
  }
  return device.connected
    ? { state: "warn" as const, label: t("Ожидают применения") }
    : { state: "muted" as const, label: t("Ожидают подключения") };
}

export function Icon({ name, size = 22 }: { name: string; size?: number }) {
  const paths: Record<string, React.ReactNode> = {
    monitor: (
      <>
        <rect x="3" y="4" width="18" height="13" rx="2" />
        <path d="M8 21h8M12 17v4" />
      </>
    ),
    image: (
      <>
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <circle cx="8.5" cy="8.5" r="1.5" />
        <path d="m21 15-5-5L5 21" />
      </>
    ),
    database: (
      <>
        <ellipse cx="12" cy="5" rx="9" ry="3" />
        <path d="M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5M3 12c0 1.7 4 3 9 3s9-1.3 9-3" />
      </>
    ),
    wifi: (
      <>
        <path d="M5 12.55a11 11 0 0 1 14.08 0M1.42 9a16 16 0 0 1 21.16 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01" />
      </>
    ),
    refresh: (
      <>
        <path d="M20 7v5h-5M4 17v-5h5" />
        <path d="M5.64 9A7 7 0 0 1 18 7l2 5M4 12l2 5a7 7 0 0 0 12.36-2" />
      </>
    ),
    info: (
      <>
        <circle cx="12" cy="12" r="9" />
        <path d="M12 11v5M12 8h.01" />
      </>
    ),
    sun: (
      <>
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2m0 16v2M4.93 4.93l1.41 1.41m11.32 11.32 1.41 1.41M2 12h2m16 0h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
      </>
    ),
    calendar: (
      <>
        <rect x="3" y="5" width="18" height="16" rx="2" />
        <path d="M16 3v4M8 3v4M3 11h18" />
      </>
    ),
    train: (
      <>
        <rect x="5" y="3" width="14" height="16" rx="3" />
        <path d="M8 7h8M8 12h.01M16 12h.01M8 19l-2 3m10-3 2 3M5 16h14" />
      </>
    ),
    heart: (
      <path d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8l1.1 1.1L12 21l7.8-7.5 1.1-1.1a5.5 5.5 0 0 0-.1-7.8Z" />
    ),
    tasks: (
      <>
        <path d="m4 6 1.5 1.5L8 5M11 6h9M4 12l1.5 1.5L8 11m3 1h9M4 18l1.5 1.5L8 17m3 1h9" />
      </>
    ),
    news: (
      <>
        <path d="M4 3h12l4 4v14H4zM16 3v5h4M8 12h8M8 16h8" />
      </>
    ),
    home: (
      <>
        <path d="m3 10 9-7 9 7M5 9v12h14V9m-9 12v-7h4v7" />
      </>
    ),
    download: (
      <>
        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
      </>
    ),
    logout: (
      <>
        <path d="M10 17l5-5-5-5M15 12H3" />
        <path d="M12 3h7a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-7" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    close: <path d="m18 6-12 12M6 6l12 12" />,
    clock: (
      <>
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5l3 2" />
      </>
    ),
    restart: (
      <>
        <path d="M3 12a9 9 0 1 0 2.64-6.36L3 8" />
        <path d="M3 3v5h5" />
      </>
    ),
    trash: (
      <>
        <path d="M3 6h18M8 6V4h8v2m3 0-1 14H6L5 6m4 4v6m6-6v6" />
      </>
    ),
    lock: (
      <>
        <rect x="4" y="10" width="16" height="11" rx="2" />
        <path d="M8 10V7a4 4 0 0 1 8 0v3" />
      </>
    ),
    spark: (
      <>
        <path d="m12 3 1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3ZM19 16l1 2.5 2.5 1-2.5 1L19 23l-1-2.5-2.5-1 2.5-1 1-2.5Z" />
      </>
    ),
  };
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {paths[name] ?? paths.info}
    </svg>
  );
}

function labelControls(node: React.ReactNode, label: string): React.ReactNode {
  if (
    !React.isValidElement<{
      children?: React.ReactNode;
      "aria-label"?: string;
    }>(node)
  )
    return node;
  const type = node.type;
  if (
    typeof type === "string" &&
    ["input", "select", "textarea"].includes(type)
  ) {
    return React.cloneElement(node, {
      "aria-label": node.props["aria-label"] ?? label,
    });
  }
  if (typeof type === "function")
    return React.cloneElement(node, {
      "aria-label": node.props["aria-label"] ?? label,
    });
  if (node.props.children)
    return React.cloneElement(
      node,
      {},
      React.Children.map(node.props.children, (child) =>
        labelControls(child, label),
      ),
    );
  return node;
}
export function Field({
  label,
  children,
  help,
  className = "",
}: {
  label: string;
  children: React.ReactNode;
  help?: React.ReactNode;
  className?: string;
}) {
  const t = useT();
  const labelId = React.useId();
  return (
    <div className={`field ${className}`}>
      <div id={labelId} className="field-label">
        {t(label)}
      </div>
      <div className="field-control">
        {React.Children.map(children, (child) => labelControls(child, t(label)))}
        {help && <small>{typeof help === "string" ? t(help) : help}</small>}
      </div>
    </div>
  );
}
export function TextInput(props: React.InputHTMLAttributes<HTMLInputElement>) {
  const t = useT();
  return <input {...props} placeholder={props.placeholder ? t(props.placeholder) : undefined} aria-label={props["aria-label"] ? t(props["aria-label"]) : undefined} />;
}
export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  const t = useT();
  return <select {...props} aria-label={props["aria-label"] ? t(props["aria-label"]) : undefined}>{props.children}</select>;
}
export function Toggle({
  label,
  checked,
  onChange,
  disabled = false,
}: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  const t = useT();
  return (
    <label className={`toggle ${disabled ? "disabled" : ""}`}>
      <span>{t(label)}</span>
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <i aria-hidden="true" />
    </label>
  );
}
export function Segmented<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: [T, string][];
  onChange: (value: T) => void;
}) {
  const t = useT();
  return (
    <div className="segmented" role="group">
      {options.map(([key, label]) => (
        <button
          key={key}
          type="button"
          aria-pressed={value === key}
          className={value === key ? "selected" : ""}
          onClick={() => onChange(key)}
        >
          {t(label)}
        </button>
      ))}
    </div>
  );
}
export function SectionHeading({
  children,
  detail,
}: {
  children: React.ReactNode;
  detail?: React.ReactNode;
}) {
  return (
    <div className="section-heading">
      <h2>{children}</h2>
      {detail}
    </div>
  );
}
export function StatusPill({
  state,
  label,
}: {
  state: "good" | "warn" | "bad" | "muted";
  label: string;
}) {
  const t = useT();
  return (
    <span className={`status-pill ${state}`}>
      <i />
      {t(label)}
    </span>
  );
}
export function servicePill(status: Status | undefined, t: (message: string) => string): React.ReactNode {
  const state = status?.state ?? "unknown";
  return (
    <StatusPill
      state={
        state === "ready"
          ? "good"
          : state === "error"
            ? "bad"
            : state === "unconfigured"
              ? "warn"
              : "muted"
      }
      label={t(statusLabels[state])}
    />
  );
}
export function SecretField({
  label,
  present,
  value,
  onChange,
  onRemove,
  help,
}: {
  label: string;
  present: boolean;
  value: string | null;
  onChange: (v: string) => void;
  onRemove: () => void;
  help?: string;
}) {
  const t = useT();
  const markedRemove = value === null;
  return (
    <Field
      label={label}
      help={
        help ??
        (present && value === ""
          ? "Ключ сохранён. Пустое поле оставляет прежний ключ."
          : "Ключ доступен только для записи и не возвращается сервером.")
      }
    >
      <div className="secret-row">
        <TextInput
          type="password"
          autoComplete="new-password"
          value={value ?? ""}
          placeholder={
            markedRemove
              ? "Будет удалён при сохранении"
              : present && value === ""
                ? "Сохранён"
                : "Введите ключ"
          }
          onChange={(e) => onChange(e.target.value)}
          aria-label={label}
        />
        <button
          type="button"
          className="button quiet"
          disabled={!present || markedRemove}
          onClick={onRemove}
        >
          {" "}{t("Удалить ключ")}{" "}</button>
      </div>
    </Field>
  );
}
