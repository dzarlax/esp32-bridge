import { describe, expect, it } from "vitest";
import { numberError } from "./validation";

describe("photo timing limits", () => {
  it.each([5, 29, 3601, 30.5, NaN])("explains invalid idle delay %s", (value) => {
    expect(numberError(value, 30, 3600, "сек")).toBe("Введите целое число от 30 до 3600 сек.");
  });
  it.each([30, 300, 3600])("accepts valid idle delay %s", (value) => {
    expect(numberError(value, 30, 3600, "сек")).toBeNull();
  });
});
