import { describe, expect, it } from "vitest";
import type { Envelope } from "./types";
import { applicationState, hasCurrentApplicationAck, sameSettings } from "./settings-apply-status";

function envelope(overrides: {
  revision?: number;
  settings?: { screen?: Partial<Envelope["settings"]["screen"]> };
  device?: Partial<Envelope["device"]>;
} = {}): Envelope {
  const base = {
    device_id: "a".repeat(32), revision: 4,
    settings: { screen: { locale: "ru" } },
    device: { connected: true, last_seen: "2026-10-30T10:00:00Z", applied_revision: 4, applied_locale: "ru", capabilities: ["locale"] },
  };
  return {
    ...base,
    ...overrides,
    settings: { ...base.settings, ...overrides.settings, screen: { ...base.settings.screen, ...overrides.settings?.screen } },
    device: { ...base.device, ...overrides.device },
  } as Envelope;
}

describe("settings application status", () => {
  it("requires a positive matching revision, a connection timestamp, and a matching locale acknowledgement", () => {
    expect(hasCurrentApplicationAck(envelope())).toBe(true);
    expect(hasCurrentApplicationAck(envelope({ revision: 0 }))).toBe(false);
    expect(hasCurrentApplicationAck(envelope({ device: { ...envelope().device, applied_revision: 3 } }))).toBe(false);
    expect(hasCurrentApplicationAck(envelope({ device: { ...envelope().device, last_seen: null } }))).toBe(false);
    expect(hasCurrentApplicationAck(envelope({ settings: { screen: { locale: "en" } } }))).toBe(false);
    expect(hasCurrentApplicationAck(envelope({
      settings: { screen: { locale: "en" } },
      device: { ...envelope().device, applied_locale: "en" },
    }))).toBe(true);
  });

  it("keeps offline devices in the waiting state and shows overdue confirmation only while online", () => {
    const pendingOffline = envelope({ device: { ...envelope().device, connected: false, applied_revision: 0 } });
    const pendingOnline = envelope({ device: { ...envelope().device, applied_revision: 0 } });
    expect(applicationState(pendingOffline, 1_000, 100_000)).toBe("waiting-offline");
    expect(applicationState(pendingOnline, 1_000, 100_000)).toBe("not-confirmed");
    expect(applicationState(pendingOnline, 1_000, 40_000)).toBe("waiting");
  });

  it("compares settings snapshots without including write-only credentials", () => {
    expect(sameSettings({ screen: { name: "Hall" } } as Envelope["settings"], { screen: { name: "Hall" } } as Envelope["settings"])).toBe(true);
    expect(sameSettings({ screen: { name: "Hall" } } as Envelope["settings"], { screen: { name: "Office" } } as Envelope["settings"])).toBe(false);
  });
});
