import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HomeEntityPicker } from "./service-pages";
import { LocaleProvider } from "./i18n";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("Home Assistant entity picker", () => {
  it("searches friendly names and IDs in domain tabs and selects explicitly", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ complete: true, items: [
      { id: "light.kitchen", name: "Kitchen", domain: "light", available: true },
      { id: "climate.den", name: "Den", domain: "climate", available: true },
      { id: "sensor.temperature", name: "Temperature", domain: "sensor", available: true },
    ] }), { status: 200 })));
    const onChange = vi.fn();
    render(<LocaleProvider><HomeEntityPicker deviceId={"a".repeat(32)} selected={{ light: [], climate: [], sensor: [] }} onChange={onChange} /></LocaleProvider>);
    await screen.findByText("Kitchen");
    fireEvent.change(screen.getByLabelText("Поиск по имени или ID"), { target: { value: "light.kitchen" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Kitchen.*light\.kitchen/ }));
    expect(onChange).toHaveBeenCalledWith({ light: ["light.kitchen"], climate: [], sensor: [] });
    fireEvent.change(screen.getByLabelText("Поиск по имени или ID"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("tab", { name: "Климат" }));
    expect(screen.getByText("Den")).toBeTruthy();
  });

  it("retains selected IDs after fetch failure and labels unavailable entities", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ complete: true, items: [
      { id: "light.old", name: "Old", domain: "light", available: false },
    ] }), { status: 200 })));
    const onChange = vi.fn();
    render(<LocaleProvider><HomeEntityPicker deviceId={"b".repeat(32)} selected={{ light: ["light.old", "light.missing"], climate: [], sensor: [] }} onChange={onChange} /></LocaleProvider>);
    await screen.findByText("Old");
    expect(screen.getByText("Недоступна в Home Assistant")).toBeTruthy();
    expect(screen.getByText(/light\.missing/)).toBeTruthy();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("keeps selected IDs visible when the catalog request fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "catalog_forbidden" }), { status: 502 })));
    const onChange = vi.fn();
    render(<LocaleProvider><HomeEntityPicker deviceId={"c".repeat(32)} selected={{ light: ["light.saved"], climate: [], sensor: [] }} onChange={onChange} /></LocaleProvider>);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Источник отказал"));
    expect(screen.getByText(/light\.saved/)).toBeTruthy();
    expect(onChange).not.toHaveBeenCalled();
  });
});
