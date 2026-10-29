import { afterEach, describe, expect, it, vi } from "vitest";
import { AdminApi, ApiError, isRevisionConflict } from "./api";
import { formatUptime, isCommandFinal, type Envelope, type SharedConnections } from "./types";

afterEach(() => vi.unstubAllGlobals());

describe("AdminApi", () => {
  it("uses same-origin cookies and session CSRF for settings writes", async () => {
    const deviceId = "a".repeat(32);
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify({ device_id: deviceId, revision: 8, settings: { services: {} } }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const api = new AdminApi();
    api.setCsrf("session-csrf");
    await api.save(deviceId, {} as never, 7, { wifi_ssid: "mesh" });
    const [, init] = fetchMock.mock.calls[0] as unknown as [
      RequestInfo | URL,
      RequestInit,
    ];
    const headers = new Headers(init.headers);
    expect(init.credentials).toBe("same-origin");
    expect(headers.get("X-CSRF-Token")).toBe("session-csrf");
    expect(headers.get("If-Match")).toBe('"7"');
    expect(JSON.parse(String(init.body))).toEqual({
      settings: {},
      network_credentials: { wifi_ssid: "mesh" },
    });
    expect(String(fetchMock.mock.calls[0][0])).toBe(`/api/admin/v1/devices/${deviceId}/settings`);
  });

  it("retains safe server error details and status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: "revision conflict" }), {
            status: 409,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    const error = await new AdminApi().settings("a".repeat(32)).catch((value) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 409 });
    expect((error as ApiError).message).toBe("revision conflict");
  });

  it("distinguishes a revision conflict from an offline network change", () => {
    expect(isRevisionConflict(new ApiError("revision_conflict", 409, { error: "revision_conflict" }))).toBe(true);
    expect(isRevisionConflict(new ApiError("device_offline", 409, { error: "device_offline" }))).toBe(false);
  });

  it("saves shared connections with their own revision and explicit disconnect flag", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ revision: 9, connections: {} }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const api = new AdminApi();
    api.setCsrf("csrf-token");
    const connections = { immich: { base_url: "" }, health: { base_url: "" }, tasks: {}, news: { base_url: "" }, home: { base_url: "" }, calendar: { base_url: "" }, transport: { base_url: "" } } as SharedConnections;
    await api.saveConnections(connections, 8, { immich_api_key: null }, true);
    const [path, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe("/api/admin/v1/connections");
    expect(new Headers(init.headers).get("If-Match")).toBe('"8"');
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("csrf-token");
    expect(JSON.parse(String(init.body))).toEqual({ connections, secrets: { immich_api_key: null }, confirm_disconnect: true });
  });

  it("handles a plain-text auth or runtime error without parsing it as JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response("not authorized", {
            status: 401,
            headers: { "Content-Type": "text/plain" },
          }),
      ),
    );
    const error = await new AdminApi().session().catch((value) => value);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(401);
    expect((error as ApiError).message).toBe("not authorized");
  });

  it("normalizes omitted optional service lists to empty arrays", async () => {
    const payload = {
      device_id: "a".repeat(32),
      settings: {
        services: {
          calendar: { enabled: true },
          transport: { enabled: true },
          home: { enabled: false },
        },
      },
    } as unknown as Envelope;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify(payload), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    const result = await new AdminApi().settings("a".repeat(32));
    expect(result.settings.services.calendar.ids).toEqual([]);
    expect(result.settings.services.transport.ids).toEqual([]);
    expect(result.settings.services.home.sensors).toEqual([]);
    expect(result.settings.services.home.lights).toEqual([]);
    expect(result.settings.services.home.climates).toEqual([]);
  });

  it("does not treat active device commands as final", () => {
    expect(
      isCommandFinal({
        id: "x",
        action: "restart",
        state: "accepted",
        created_at: "",
        expires_at: "",
      }),
    ).toBe(false);
    expect(
      isCommandFinal({
        id: "x",
        action: "restart",
        state: "succeeded",
        created_at: "",
        expires_at: "",
      }),
    ).toBe(true);
    expect(
      isCommandFinal({
        id: "x",
        action: "restart",
        state: "interrupted",
        created_at: "",
        expires_at: "",
      }),
    ).toBe(true);
  });
});

describe("display state formatting", () => {
  it("formats reported uptime without inventing a value", () => {
    expect(formatUptime(90061)).toBe("1 д 1 ч");
    expect(formatUptime(0)).toBe("0 мин");
    expect(formatUptime(Number.NaN)).toBe("—");
  });
});
