// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { MailWorkspaceContext, type MailWorkspace } from "../desktop/MailWorkspaceContext";
import { EmailMessageCard } from "./emailMessage";

it("keeps the original download controls and routes originals and attachments through the desktop workspace", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const download = vi.fn(async () => {});
  const network = vi.fn(() => { throw new Error("Renderer must not download desktop mail over HTTP"); });
  vi.stubGlobal("fetch", network);
  const workspace: MailWorkspace = { identity: 1, enabled: true, sendEnabled: true, attachmentsEnabled: true,
    listFiles: vi.fn(), conversationID: "conversation", onFileSaved: vi.fn(), download };
  const error = vi.fn(), host = document.createElement("div"), root = createRoot(host);
  try {
    await act(async () => root.render(<MailWorkspaceContext.Provider value={workspace}><EmailMessageCard
      mail={{ id: "mail", mailbox_id: "box", version: 1, receiving_address: "self@example.test", direction: "inbound", from: "self@example.test", to: [], cc: [], subject: "Fixture", arrived_at: "2026-10-10T00:00:00Z", viewed: true, original_available: true,
        attachments: [{ id: "part", name: "fixture.txt", size: 158, available: true }] }}
      viewed text={dictionaries.en} language="en" busy={false} onReanalyze={vi.fn()} onError={error} />
    </MailWorkspaceContext.Provider>));
    const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent?.includes(label))!;
    await act(async () => button(dictionaries.en.email.originalDownload).click());
    await act(async () => button("fixture.txt").click());
    expect(download.mock.calls).toEqual([["box", "mail", ""], ["box", "mail", "part"]]);
    expect(network).not.toHaveBeenCalled(); expect(error).not.toHaveBeenCalled();
  } finally { await act(async () => root.unmount()); vi.unstubAllGlobals(); }
});
