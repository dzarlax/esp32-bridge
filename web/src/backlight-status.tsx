import { useEffect, useState } from "react";
import { useT } from "./i18n";
import type { Device } from "./types";

export function BacklightStatus({ device, transportFresh }: { device: Device; transportFresh: boolean }) {
  const t = useT();
  const [, setClock] = useState(0);
  const now = Date.now();
  const report = device.backlight_report;
  const hasReport = report !== undefined;
  useEffect(() => {
    if (!hasReport) return;
    const timer = window.setInterval(() => setClock(value => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, [hasReport]);
  if (!report) return null;
  const seen = device.last_seen === null ? NaN : Date.parse(device.last_seen);
  const age = now - seen;
  const fresh = transportFresh && device.connected && Number.isFinite(age) && age >= 0 && age <= 20000;
  const known = fresh && report.output_known && report.last_command !== null;
  const remaining = Math.max(0, Math.ceil(((report.restore_deadline_uptime_ms ?? 0) - report.sample_uptime_ms - age) / 1000));
  let title = t("Текущее состояние подсветки неизвестно");
  if (!fresh) title = t("Нет свежего подтверждения подсветки");
  else if (report.phase === "error") title = t("Ошибка записи подсветки");
  else if (known && report.phase === "preview") title = remaining > 0 ? t("Тест подсветки выполняется") : t("Ожидаем подтверждения возврата");
  else if (known && report.phase === "restored" && report.last_command === report.return_command) title = t("Команда возврата выполнена");
  else if (known && report.phase === "reference") title = t("Запись команды подсветки подтверждена");
  return <section className="backlight-status" aria-label={t("Обратная связь подсветки")}>
    <h3>{t("Обратная связь подсветки")}</h3>
    <p role="status"><strong>{title}</strong></p>
    {report.last_command !== null && <p>{t("Последняя успешная команда")}: <strong>{report.last_command}</strong></p>}
    {!known && report.last_command !== null && <p>{t("Текущее значение неизвестно; предыдущая команда показана как история.")}</p>}
    {report.test_command != null && <p>{t("Команда теста")}: {report.test_command} · {t("Команда возврата")}: {report.return_command}</p>}
    {known && report.phase === "preview" && remaining > 0 && <p>{t("Осталось приблизительно")}: {remaining} {t("сек")}</p>}
    <small>{t("Это подтверждение записи I²C, а не измерение яркости.")}{" "}
      {Number.isFinite(age) && age >= 0 && <>{t("Возраст последнего ответа")}: {Math.floor(age / 1000)} {t("сек")}.</>}
    </small>
  </section>;
}
