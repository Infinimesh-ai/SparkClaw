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

it("deletes a batch after one confirmation and reconciles what was deleted before a failure", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const failure = new Error("gateway unavailable");
  const remove = vi.spyOn(api, "deleteSession").mockImplementation(async (id: string) => {
    if (id === "broken") throw failure;
    return { id, title: id, created_at: "", updated_at: "" };
  });
  vi.spyOn(api, "sessions").mockResolvedValue({ sessions: [
    { id: "keep", title: "Keep", created_at: "", updated_at: "" },
    { id: "broken", title: "Broken", created_at: "", updated_at: "" }
  ] });
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const options: Parameters<typeof useSessionCrud>[0] = {
    activeSession: "first", text: dictionaries.en,
    setError: vi.fn(), surfaceError: vi.fn(), setSessions: vi.fn(), setActiveSession: vi.fn(),
    setMessages: vi.fn(), setDraftsBySession: vi.fn(), setAttachmentsBySession: vi.fn(),
    setToolCalls: vi.fn(), setModelCalls: vi.fn(), setAuditEvents: vi.fn(), setEpisodes: vi.fn(),
    setTab: vi.fn(), setTraceRun: vi.fn(), clearSessionTarget: vi.fn(),
    refreshSession: vi.fn(async () => {}), refreshGlobal: vi.fn(async () => {})
  };
  function Harness() {
    const { deleteSessions } = useSessionCrud(options);
    return <button onClick={() => void deleteSessions(["first", "second", "broken", "after"], "Delete all 4?")}>Delete all</button>;
  }
  try {
    await act(async () => root.render(<ConfirmationProvider><Harness /></ConfirmationProvider>));
    await act(async () => host.querySelector("button")!.click());
    expect(document.body.textContent).toContain("Delete all 4?");
    await act(async () => document.querySelector<HTMLButtonElement>(".confirmationAccept")!.click());
    expect(remove.mock.calls).toEqual([["first"], ["second"], ["broken"]]);
    expect(options.clearSessionTarget).toHaveBeenCalledTimes(2);
    expect(options.setActiveSession).toHaveBeenCalledWith("keep");
    expect(options.surfaceError).toHaveBeenCalledExactlyOnceWith(failure, dictionaries.en.errors.deleteSession);
    expect(document.querySelector(".confirmationAccept")).toBeNull();
  } finally {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks(); vi.unstubAllGlobals();
  }
});
