import { afterEach, describe, expect, it, vi } from "vitest";
import ts from "typescript";
import { dateLocale, normalizeLocale, readUILocale, translationCatalogKeys, translateText, writeUILocale } from "./i18n";
import { formatUptime } from "./types";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("interface localization", () => {
  it("covers every Russian UI literal with matching English and Serbian catalog entries", () => {
    const keys = new Set<string>();
    const sourceFiles = import.meta.glob("./*.{ts,tsx}", { eager: true, query: "?raw", import: "default" }) as Record<string, string>;
    for (const [filename, source] of Object.entries(sourceFiles).filter(([name]) => !name.endsWith(".test.ts") && !name.endsWith(".test.tsx") && name !== "./i18n.tsx")) {
      const file = ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true, filename.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
      const add = (value: string) => {
        const normalized = value.replace(/\s+/g, " ").trim();
        if (/[А-Яа-яЁё]/.test(normalized)) keys.add(normalized);
      };
      const visit = (node: ts.Node) => {
        if (ts.isJsxText(node)) add(node.text);
        if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && /[А-Яа-яЁё]/.test(node.text)) add(node.text);
        ts.forEachChild(node, visit);
      };
      visit(file);
    }
    const en = new Set<string>(translationCatalogKeys("en"));
    const sr = new Set<string>(translationCatalogKeys("sr-Latn"));
    expect([...en].sort()).toEqual([...sr].sort());
    expect([...keys].filter((key) => !en.has(key)).sort()).toEqual([]);
  });

  it.each([
    ["ru", "Фоторамка", "ru-RU"],
    ["en", "Photo frame", "en-GB"],
    ["sr-Latn", "Foto-ram", "sr-Latn-RS"],
  ] as const)("uses the %s catalog and date locale", (locale, label, date) => {
    expect(translateText("Фоторамка", locale)).toBe(label);
    expect(dateLocale(locale)).toBe(date);
  });

  it.each([
    ["ru", "Запуск после бездействия: Введите целое число от 30 до 3600 сек."],
    ["en", "Start after idle: Enter a whole number from 30 to 3600 sec."],
    ["sr-Latn", "Pokreni nakon mirovanja: Unesite ceo broj od 30 do 3600 sek."],
  ] as const)("keeps parameterized validation in the %s locale", (locale, expected) => {
    expect(translateText("Запуск после бездействия: Введите целое число от 30 до 3600 сек.", locale)).toBe(expected);
  });

  it("translates parameterized help and confirmation while retaining supplied values", () => {
    expect(translateText("Здоровье: общий адрес подключения для всех дисплеев.", "en")).toBe("Health: shared connection address for all displays.");
    expect(translateText("Данные здоровья. Сохранённое значение нельзя прочитать; новое значение заменит его.", "sr-Latn")).toBe("Podaci o zdravlju. Sačuvana vrednost se ne može pročitati; nova vrednost će je zameniti.");
    expect(translateText("Устройство установит версию 2.4.1 и может перезагрузиться. Не отключайте питание.", "en")).toBe("The device will install version 2.4.1 and may restart. Do not disconnect power.");
  });

  it("defaults invalid or missing stored values to Russian", () => {
    expect(normalizeLocale(undefined)).toBe("ru");
    window.localStorage.setItem("homedash.ui.locale", "fr");
    expect(readUILocale()).toBe("ru");
  });

  it("keeps a session locale when browser storage refuses writes", () => {
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("Blocked", "SecurityError");
    });
    expect(() => writeUILocale("en")).not.toThrow();
  });

  it("localizes uptime while preserving the reported values", () => {
    expect(formatUptime(90061, "ru")).toBe("1 д 1 ч");
    expect(formatUptime(90061, "en")).toBe("1 d 1 hr");
    expect(formatUptime(90061, "sr-Latn")).toBe("1 d 1 č");
    expect(formatUptime(-1, "en")).toBe("—");
  });
});
