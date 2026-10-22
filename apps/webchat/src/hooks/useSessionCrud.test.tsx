// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { api } from "../api/client";
import { ConfirmationProvider } from "../components/confirmation";
import { dictionaries } from "../i18n";
import { useSessionCrud } from "./useSessionCrud";

it("does not delete twice when two queued confirmations are accepted during one request", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  type DeletedSession = Awaited<ReturnType<typeof api.deleteSession>>;
  let resolveDelete!: (value: DeletedSession) => void;
  const pending = new Promise<DeletedSession>(resolve => { resolveDelete = resolve; });
  const remove = vi.spyOn(api, "deleteSession").mockReturnValue(pending);
  vi.spyOn(api, "sessions").mockResolvedValue({ sessions: [{ id: "keep", title: "Keep", created_at: "", updated_at: "" }] });
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const options: Parameters<typeof useSessionCrud>[0] = {
    activeSession: "delete-me", text: dictionaries.en,
    setError: vi.fn(), surfaceError: vi.fn(), setSessions: vi.fn(), setActiveSession: vi.fn(),
    setMessages: vi.fn(), setDraftsBySession: vi.fn(), setAttachmentsBySession: vi.fn(),
    setToolCalls: vi.fn(), setModelCalls: vi.fn(), setAuditEvents: vi.fn(), setEpisodes: vi.fn(),
    setTab: vi.fn(), setTraceRun: vi.fn(), clearSessionTarget: vi.fn(),
    refreshSession: vi.fn(async () => {}), refreshGlobal: vi.fn(async () => {})
  };
  function Harness() {
    const { deleteSession } = useSessionCrud(options);
    return <button onClick={() => void deleteSession("delete-me")}>Delete</button>;
  }
  try {
    await act(async () => root.render(<ConfirmationProvider><Harness /></ConfirmationProvider>));
    const button = host.querySelector("button")!;
    await act(async () => { button.click(); button.click(); });
    await act(async () => document.querySelector<HTMLButtonElement>(".confirmationAccept")!.click());
    expect(remove).toHaveBeenCalledExactlyOnceWith("delete-me");
    expect(document.querySelector(".confirmationAccept")).not.toBeNull();
    await act(async () => document.querySelector<HTMLButtonElement>(".confirmationAccept")!.click());
    expect(remove).toHaveBeenCalledOnce();
    await act(async () => resolveDelete({ id: "delete-me", title: "Deleted", created_at: "", updated_at: "" }));
    expect(options.refreshSession).toHaveBeenCalledWith("keep");
  } finally {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks(); vi.unstubAllGlobals();
  }
});
