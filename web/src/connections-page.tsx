import { useEffect, useMemo, useRef, useState } from "react";
import type { ConnectionsEnvelope, SharedConnections, SecretKey } from "./types";
import { Field, Icon, SectionHeading, TextInput } from "./ui";

type Secrets = Partial<Record<SecretKey, string | null>>;
type Props = {
  value: ConnectionsEnvelope;
  onSave: (
    connections: SharedConnections,
    revision: number,
    secrets: Secrets,
    confirmDisconnect: boolean,
  ) => Promise<ConnectionsEnvelope>;
  onDirtyChange?: (dirty: boolean) => void;
};

const URL_FIELDS = [
  ["immich", "Immich", "Сервер фотографий"],
  ["health", "Health Dashboard", "Данные здоровья"],
  ["news", "Новости", "Лента новостей"],
  ["home", "Home Assistant", "Умный дом"],
  ["calendar", "Calendar Platform", "Календари"],
  ["transport", "Транспорт", "Расписание транспорта"],
] as const;
const SECRET_FIELDS: { key: SecretKey; title: string; label: string }[] = [
  { key: "immich_api_key", title: "Immich", label: "API key Immich" },
  { key: "health_api_key", title: "Health Dashboard", label: "API key Health Dashboard" },
  { key: "todoist_token", title: "Todoist", label: "Токен Todoist" },
  { key: "ha_token", title: "Home Assistant", label: "Токен Home Assistant" },
  { key: "calendar_platform_token", title: "Calendar Platform", label: "Токен Calendar Platform" },
];

function cloneConnections(value: SharedConnections): SharedConnections {
  return structuredClone(value);
}

export function ConnectionsPage({ value, onSave, onDirtyChange }: Props) {
  const [draft, setDraft] = useState(() => cloneConnections(value.connections));
  const [secrets, setSecrets] = useState<Secrets>({});
  const [baseRevision, setBaseRevision] = useState(value.revision);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);
  const dialogRef = useRef<HTMLElement>(null);
  const dialogCancelRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);

  const dirty = useMemo(
    () => JSON.stringify(draft) !== JSON.stringify(value.connections) || Object.keys(secrets).length > 0,
    [draft, value.connections, secrets],
  );

  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);

  useEffect(() => {
    if (value.revision === baseRevision) return;
    if (dirty) {
      setConflict(true);
      return;
    }
    setDraft(cloneConnections(value.connections));
    setBaseRevision(value.revision);
  }, [baseRevision, dirty, value]);

  useEffect(() => {
    if (!confirmDisconnect) return;
    dialogCancelRef.current?.focus();
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setConfirmDisconnect(false);
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const controls = Array.from(dialogRef.current.querySelectorAll<HTMLElement>(
        'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ));
      if (controls.length === 0) {
        event.preventDefault();
        dialogRef.current.focus();
      } else if (event.shiftKey && document.activeElement === controls[0]) {
        event.preventDefault();
        controls.at(-1)?.focus();
      } else if (!event.shiftKey && document.activeElement === controls.at(-1)) {
        event.preventDefault();
        controls[0].focus();
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      returnFocusRef.current?.focus();
    };
  }, [confirmDisconnect]);

  function reset() {
    setDraft(cloneConnections(value.connections));
    setSecrets({});
    setBaseRevision(value.revision);
    setConflict(false);
    setError("");
    setSaved(false);
    setConfirmDisconnect(false);
  }

  function hasDisconnects() {
    const removedURL = URL_FIELDS.some(([key]) => {
      const oldValue = value.connections[key].base_url;
      return Boolean(oldValue) && !draft[key].base_url.trim();
    });
    const removedSecret = Object.values(secrets).some((secret) => secret === null);
    return removedURL || removedSecret;
  }

  async function persist(confirmed: boolean) {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const next = await onSave(draft, baseRevision, secrets, confirmed);
      setDraft(cloneConnections(next.connections));
      setSecrets({});
      setBaseRevision(next.revision);
      setConflict(false);
      setConfirmDisconnect(false);
      setSaved(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Не удалось сохранить подключения.");
    } finally {
      setBusy(false);
    }
  }

  function submit(event: React.FormEvent) {
    event.preventDefault();
    if (hasDisconnects()) {
      returnFocusRef.current = event.currentTarget.querySelector("button[type=submit]");
      returnFocusRef.current?.focus();
      setConfirmDisconnect(true);
      return;
    }
    void persist(false);
  }

  return (
    <div className="connections-page">
      <div className="scope-notice" role="note">
        <Icon name="info" size={18} />
        <span><strong>Общие подключения</strong> применяются ко всем дисплеям. Изменение адреса или удаление ключа может отключить источник на нескольких устройствах.</span>
      </div>
      {conflict && (
        <div className="alert error" role="alert">
          Подключения изменились в другом сеансе. Ваши изменения сохранены в форме.
          <button className="button secondary" type="button" onClick={reset}>Загрузить актуальные данные</button>
        </div>
      )}
      {error && <div className="alert error" role="alert">{error}</div>}
      {saved && <div className="alert success" role="status">Общие подключения сохранены. Изменения затрагивают все дисплеи.</div>}
      <form onSubmit={submit}>
        <section className="settings-section">
          <SectionHeading>Адреса источников</SectionHeading>
          {URL_FIELDS.map(([key, title, label]) => (
            <Field key={key} label={label} help={`${title}: общий адрес подключения для всех дисплеев.`}>
              <TextInput
                type="url"
                value={draft[key].base_url}
                placeholder="https://"
                autoComplete="url"
                onChange={(event) => {
                  const base_url = event.target.value;
                  setDraft((current) => ({ ...current, [key]: { base_url } }));
                  setSaved(false);
                }}
              />
            </Field>
          ))}
        </section>
        <section className="settings-section">
          <SectionHeading>Ключи и токены</SectionHeading>
          {SECRET_FIELDS.map(({ key, title, label }) => {
            const present = Boolean(value.secrets_present[key]);
            const removed = secrets[key] === null;
            return (
              <Field key={key} label={label} help={`${title}. Сохранённое значение нельзя прочитать; новое значение заменит его.`}>
                <div className="shared-secret-row">
                  <TextInput
                    type="password"
                    value={typeof secrets[key] === "string" ? secrets[key] as string : ""}
                    autoComplete="new-password"
                    placeholder={present && !removed ? "Ключ сохранён — введите замену" : "Введите ключ или токен"}
                    aria-label={label}
                    onChange={(event) => {
                      const next = { ...secrets, [key]: event.target.value };
                      if (!event.target.value) delete next[key];
                      setSecrets(next);
                      setSaved(false);
                    }}
                  />
                  <span className={`badge ${present && !removed ? "good" : "muted"}`}>{removed ? "Будет удалён" : present ? "Сохранён" : "Не задан"}</span>
                  {present && (
                    <button className="button quiet" type="button" onClick={() => setSecrets((old) => ({ ...old, [key]: removed ? "" : null }))}>
                      {removed ? "Отменить удаление" : "Удалить"}
                    </button>
                  )}
                </div>
              </Field>
            );
          })}
        </section>
        <div className="connection-actions">
          <span>{dirty ? "Есть несохранённые общие изменения" : "Все дисплеи используют сохранённые подключения"}</span>
          <div>
            <button className="button secondary" type="button" onClick={reset} disabled={!dirty || busy}>Отменить</button>
            <button className="button primary" type="submit" disabled={!dirty || busy || conflict}>{busy ? "Сохранение…" : "Сохранить подключения"}</button>
          </div>
        </div>
      </form>
      {confirmDisconnect && (
        <div className="modal-backdrop" role="presentation" onClick={(event) => { if (event.target === event.currentTarget) setConfirmDisconnect(false); }}>
          <section className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="disconnect-title" aria-describedby="disconnect-description" ref={dialogRef} tabIndex={-1}>
            <div className="dialog-icon danger"><Icon name="info" size={24} /></div>
            <h2 id="disconnect-title">Отключить общий источник?</h2>
            <p id="disconnect-description">Удаление адреса или ключа повлияет на все дисплеи, которые используют это подключение. Выбранные источники на затронутых дисплеях будут отключены.</p>
            <div className="button-row">
              <button ref={dialogCancelRef} className="button secondary" type="button" onClick={() => setConfirmDisconnect(false)}>Вернуться к форме</button>
              <button className="button danger" type="button" disabled={busy} onClick={() => void persist(true)}>Подтвердить отключение</button>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}
