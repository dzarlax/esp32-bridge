import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { CatalogPicker } from "./catalog-picker";
import { LocaleProvider } from "./i18n";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
describe("catalog selection", () => {
  it("retains unavailable selections and makes only an explicit change", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [{ id: "new", name: "New" }], complete: true }), { status: 200 })));
    const onChange = vi.fn();
    render(<LocaleProvider><CatalogPicker deviceId={"a".repeat(32)} kind="calendars" selected={["old"]} onChange={onChange} /></LocaleProvider>);
    await screen.findByText(/Недоступен в списке/);
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Поиск по имени или ID"), { target: { value: "new" } });
    expect(screen.getByLabelText(/New/)).toBeTruthy();
    fireEvent.click(screen.getByLabelText(/New/));
    expect(onChange).toHaveBeenCalledWith(["old", "new"]);
  });
  it("does not clear a selection when the catalog fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "catalog_forbidden" }), { status: 403 })));
    const onChange = vi.fn();
    render(<LocaleProvider><CatalogPicker deviceId={"a".repeat(32)} kind="albums" selected={["old"]} onChange={onChange} /></LocaleProvider>);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("отказал"));
    expect(screen.getByText(/Сохранённый выбор; список не загружен/)).toBeTruthy();
    expect(onChange).not.toHaveBeenCalled();
  });
});
