import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConnectionsPage } from "./connections-page";
import type { ConnectionsEnvelope } from "./types";

const emptyConnections: ConnectionsEnvelope = {
  revision: 4,
  connections: {
    immich: { base_url: "https://photos.example" },
    health: { base_url: "" },
    tasks: {},
    news: { base_url: "" },
    home: { base_url: "" },
    calendar: { base_url: "" },
    transport: { base_url: "" },
  },
  secrets_present: {
    immich_api_key: true,
    health_api_key: false,
    todoist_token: false,
    ha_token: false,
    calendar_platform_token: false,
  },
};

afterEach(cleanup);

describe("shared connections", () => {
  it("saves shared settings using the shared revision and never refills a secret", async () => {
    const onSave = vi.fn(async (connections, revision, secrets, confirmed) => ({
      ...emptyConnections,
      revision: revision + 1,
      connections,
      secrets_present: { ...emptyConnections.secrets_present, ...secrets, immich_api_key: true },
      confirmed,
    }));
    render(<ConnectionsPage value={emptyConnections} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("API key Immich"), { target: { value: "new-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить подключения" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce());
    expect(onSave.mock.calls[0][1]).toBe(4);
    expect(onSave.mock.calls[0][2]).toEqual({ immich_api_key: "new-secret" });
    expect(onSave.mock.calls[0][3]).toBe(false);
    expect((screen.getByLabelText("API key Immich") as HTMLInputElement).value).toBe("");
  });

  it("requires a confirmation before removing a shared URL or credential", async () => {
    const onSave = vi.fn(async (connections, revision, _secrets, confirmed) => ({ ...emptyConnections, revision: revision + 1, connections, confirmed }));
    render(<ConnectionsPage value={emptyConnections} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Сервер фотографий"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Сохранить подключения" }));
    expect(onSave).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Подтвердить отключение" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce());
    expect(onSave.mock.calls[0][3]).toBe(true);
  });

  it("closes the disconnect confirmation on Escape and restores focus", () => {
    render(<ConnectionsPage value={emptyConnections} onSave={vi.fn()} />);
    const saveButton = screen.getByRole("button", { name: "Сохранить подключения" });
    fireEvent.change(screen.getByLabelText("Сервер фотографий"), { target: { value: "" } });
    fireEvent.click(saveButton);
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(document.activeElement).toBe(saveButton);
  });
});
