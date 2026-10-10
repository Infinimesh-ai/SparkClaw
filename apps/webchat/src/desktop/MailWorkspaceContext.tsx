import { createContext, useContext } from "react";
import type { LocalFile } from "./clientStore";

// Presentation adapters only: the backend owns mail business state and the
// desktop main process verifies every local file and transferred object.
export type MailWorkspace = {
  identity: number;
  enabled: boolean;
  sendEnabled: boolean;
  loginEnabled?: boolean;
  attachmentsEnabled: boolean;
  listFiles: () => Promise<LocalFile[]>;
  conversationID: string;
  onFileSaved: () => Promise<void>;
  download: (mailboxID: string, mailID: string, partID: string) => Promise<void>;
};

export const MailWorkspaceContext = createContext<MailWorkspace | null>(null);
export const useMailWorkspace = () => useContext(MailWorkspaceContext);
