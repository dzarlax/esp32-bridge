import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BacklightStatus } from "./backlight-status";
import { LocaleProvider, type UILocale } from "./i18n";
import type { BacklightReport, Device } from "./types";

const now = Date.parse("2026-10-31T12:00:00Z");
const preview: BacklightReport = { version: 1, phase: "preview", last_command: 70, output_known: true,
  test_command: 70, return_command: 50, sample_uptime_ms: 12000, restore_deadline_uptime_ms: 20000, error: "" };
function device(report: BacklightReport | undefined = preview): Device {
  return { connected: true, last_seen: new Date(now).toISOString(), applied_revision: 6, firmware: "1.0.34",
    ip: "", ssid: "", uptime_seconds: 12, time_synced: true, capabilities: [], error: "", command: null, backlight_report: report };
}
function view(d: Device, fresh = true) { return <LocaleProvider><BacklightStatus device={d} transportFresh={fresh} /></LocaleProvider>; }
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(now); localStorage.clear(); localStorage.setItem("homedash.ui.locale", "ru"); });
afterEach(() => { cleanup(); vi.useRealTimers(); localStorage.clear(); });
it("waits for an actual restore report after the countdown expires", () => {
  const { rerender } = render(view(device()));
  expect(screen.getByRole("status").textContent).toContain("Тест подсветки выполняется");
  expect(screen.getByText(/Осталось приблизительно/).textContent).toContain("8");
  act(() => vi.advanceTimersByTime(8000));
  expect(screen.getByRole("status").textContent).toContain("Ожидаем подтверждения возврата");
  expect(screen.queryByText("Команда возврата выполнена")).toBeNull();
  rerender(view({ ...device({ ...preview, phase: "restored", last_command: 50, sample_uptime_ms: 20000 }), last_seen: new Date(Date.now()).toISOString() }));
  expect(screen.getByRole("status").textContent).toContain("Команда возврата выполнена");
});
it("accepts a fresh heartbeat between timer ticks without flashing stale", () => {
  const { rerender } = render(view(device()));
  vi.setSystemTime(now + 500);
  rerender(view({ ...device(), last_seen: new Date(Date.now()).toISOString() }));
  expect(screen.getByRole("status").textContent).toContain("Тест подсветки выполняется");
});
it("expires stale telemetry without additional network requests", () => {
  render(view(device()));
  act(() => vi.advanceTimersByTime(21000));
  expect(screen.getByRole("status").textContent).toContain("Нет свежего подтверждения");
  expect(screen.getByText(/предыдущая команда/)).toBeTruthy();
});
it.each(["failed-read", "offline", "future", "invalid"])("does not claim a current value for %s", reason => {
  const d = device({ ...preview, phase: "restored", last_command: 50 });
  if (reason === "offline") d.connected = false;
  if (reason === "future") d.last_seen = new Date(now + 1000).toISOString();
  if (reason === "invalid") d.last_seen = "invalid";
  render(view(d, reason !== "failed-read"));
  expect(screen.getByRole("status").textContent).toContain("Нет свежего подтверждения");
  expect(screen.queryByText("Команда возврата выполнена")).toBeNull();
});
it("keeps the last ACK as history after a write failure and clears it on reboot", () => {
  const { rerender } = render(view(device({ ...preview, phase: "error", output_known: false, error: "write_failed" })));
  expect(screen.getByRole("status").textContent).toContain("Ошибка записи");
  expect(screen.getByText(/Последняя успешная команда/).textContent).toContain("70");
  rerender(view(device({ ...preview, phase: "unknown", last_command: null, output_known: false, test_command: null, return_command: null, restore_deadline_uptime_ms: null, error: "" })));
  expect(screen.getByRole("status").textContent).toContain("неизвестно");
  expect(screen.queryByText(/Последняя успешная команда/)).toBeNull();
});
it("keeps older firmware compatible", () => {
  const d = device(); delete d.backlight_report;
  const { container } = render(view(d)); expect(container.textContent).toBe("");
});
it.each<[UILocale, string]>([["ru", "Обратная связь подсветки"], ["en", "Backlight feedback"], ["sr-Latn", "Povratne informacije o osvetljenju"]])("localizes feedback in %s", (locale, label) => {
  localStorage.setItem("homedash.ui.locale", locale);
  render(view(device())); expect(screen.getByRole("heading").textContent).toBe(label);
});
