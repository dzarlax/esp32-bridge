import { describe, expect, it } from "vitest";
import type { Settings } from "./types";
import { hasUnresolvedHomeFavorite, homeEntityLengthError, numberError } from "./validation";

describe("photo timing limits", () => {
  it.each([5, 29, 3601, 30.5, NaN])("explains invalid idle delay %s", (value) => {
    expect(numberError(value, 30, 3600, "сек")).toBe("Введите целое число от 30 до 3600 сек.");
  });
  it.each([30, 300, 3600])("accepts valid idle delay %s", (value) => {
    expect(numberError(value, 30, 3600, "сек")).toBeNull();
  });
});

describe("Home Assistant entity length limits", () => {
  const home = (lights: string[] = [], climates: string[] = [], sensors: string[] = []) => ({ enabled: false, base_url: "", lights, climates, sensors }) as Settings["services"]["home"];

  it("rejects new action entity IDs beyond the device buffer while preserving saved IDs", () => {
    const longLightID = `light.${"a".repeat(42)}`;
    expect(homeEntityLengthError(home([longLightID]), home())).toBe("Home Assistant entity ID exceeds device limit");
    expect(homeEntityLengthError(home([longLightID]), home([longLightID]))).toBeNull();
  });

  it("uses the Bridge settings limit for sensor IDs", () => {
    expect(homeEntityLengthError(home([], [], [`sensor.${"a".repeat(122)}`]), home())).toBe("Home Assistant entity ID exceeds device limit");
  });
});

describe("dependent Home favorites", () => {
  const favorite = { entity_id: "light.sofa", kind: "light" as const, label: "Sofa" };
  const home = (lights: string[], favorites = [favorite]) => ({ enabled: true, base_url: "", lights, climates: [], sensors: [], favorites });
  it("requires resolving a favorite when removing its configured entity", () => {
    expect(hasUnresolvedHomeFavorite(home([]), home(["light.sofa"]))).toBe(true);
    expect(hasUnresolvedHomeFavorite(home([], []), home(["light.sofa"]))).toBe(false);
  });
  it("preserves an unchanged legacy orphan during unrelated edits", () => {
    expect(hasUnresolvedHomeFavorite(home([]), home([]))).toBe(false);
    expect(hasUnresolvedHomeFavorite(home([], [{ ...favorite, label: "Changed" }]), home([]))).toBe(true);
  });
});
