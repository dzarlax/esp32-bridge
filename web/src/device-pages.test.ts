import { describe, expect, it } from "vitest";
import { hasCurrentFirmwareObservation, otaStageIsHistorical, otaStageText, sameFirmwareVersion, shouldShowCommandError } from "./device-pages";

describe("OTA stage presentation", () => {
  it("hides stale progress stages after success and labels retained outcomes as historical", () => {
    expect(otaStageText({ state: "succeeded", stage: "writing" })).toBeNull();
    expect(otaStageText({ state: "unknown", stage: "writing" })).toBe("Запись");
    expect(otaStageIsHistorical("unknown")).toBe(true);
    expect(otaStageIsHistorical("failed")).toBe(true);
    expect(otaStageText({ state: "failed", stage: "new-stage" })).toBe("Этап неизвестен");
    expect(otaStageText({ state: "accepted", stage: "rebooting" })).toBe("Перезагрузка");
  });
});

describe("firmware version observations", () => {
  it("normalizes only whitespace and an optional v prefix", () => {
    expect(sameFirmwareVersion(" v1.2.3 ", "1.2.3")).toBe(true);
    expect(sameFirmwareVersion("1.2.3+build.4", "1.2.3")).toBe(false);
  });

  it("requires a fresh online observation and exact normalized version match", () => {
    expect(hasCurrentFirmwareObservation(true, "v1.2.3", "1.2.3")).toBe(true);
    expect(hasCurrentFirmwareObservation(true, "1.2.2", "1.2.3")).toBe(false);
    expect(hasCurrentFirmwareObservation(false, "1.2.3", "1.2.3")).toBe(false);
    expect(hasCurrentFirmwareObservation(true, "1.2.3", "")).toBe(false);
  });

  it("does not suggest retry when live telemetry already reports the OTA target", () => {
    const command = { id: "a", action: "ota_install", state: "failed", created_at: "", expires_at: "", error: "ota_version_not_changed", target_version: "v1.2.3" } as const;
    expect(shouldShowCommandError(command, true, "1.2.3")).toBe(false);
    expect(shouldShowCommandError(command, true, "1.2.2")).toBe(true);
    expect(shouldShowCommandError(command, false, "1.2.3")).toBe(true);
  });
});
