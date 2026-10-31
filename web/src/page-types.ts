import type { Dispatch, SetStateAction } from "react";
import type {
  Command,
  Envelope,
  Session,
  Settings,
  UpdateInfo,
} from "./types";
import type { Ask } from "./ui";

export type PageProps = {
  draft: Settings;
  edit: (value: Settings) => void;
  save: () => Promise<string | null>;
  dirty: boolean;
  envelope: Envelope;
  deviceId: string;
  onOpenConnections: () => void;
  setServiceDetail: (key: keyof Settings["services"]) => void;
  serviceDetail: keyof Settings["services"];
  setNetworkCredentials: Dispatch<SetStateAction<Record<string, string>>>;
  networkCredentials: Record<string, string>;
  networkEditing: boolean;
  setNetworkEditing: (v: boolean) => void;
  updates: UpdateInfo | null;
  firmwareObservationFresh: boolean;
  deviceObservationFresh?: boolean;
  command: Command | null;
  pendingCommand: boolean;
  ask: (ask: Ask) => void;
  busy: boolean;
  identity?: Session["identity"];
  refreshStatus: () => Promise<void>;
  downloadDiagnostics: () => Promise<void>;
  runAction: (action: Ask["action"], percent?: number) => Promise<void>;
};
