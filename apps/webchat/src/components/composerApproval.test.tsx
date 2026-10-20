// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ComposerApproval } from "./composerApproval";
import { SessionComposerApprovals } from "./sessionComposerApprovals";
import { LocalComposerApprovals } from "../desktop/localComposerApprovals";
import { dictionaries } from "../i18n";
import type { Approval } from "../api/types";
import type { LocalTask } from "../desktop/clientStore";

let host: HTMLDivElement, root: Root;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers(); vi.unstubAllGlobals(); });
const choose = (value: string) => act(async () => host.querySelector<HTMLInputElement>(`input[value="${value}"]`)!.click());
const submit = () => act(async () => host.querySelector<HTMLButtonElement>("footer button")!.click());

it("requires an explicit yes/no selection, preserves a failed decision and retries only that choice", async () => {
  const decide = vi.fn().mockRejectedValueOnce(new Error("Receipt not confirmed")).mockResolvedValue(undefined);
  await act(async () => root.render(<ComposerApproval language="zh" summary="发送这封邮件？" details={<pre>Recipient and body</pre>} onDecide={decide} />));
  expect(host.querySelectorAll('input[type="radio"]')).toHaveLength(2);
  expect(host.querySelectorAll('input:checked')).toHaveLength(0);
  expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(true);
  await choose("approve"); expect(decide).not.toHaveBeenCalled();
  await submit(); expect(decide).toHaveBeenCalledExactlyOnceWith("approve");
  expect(host.querySelector('[role="alert"]')?.textContent).toBe("Receipt not confirmed");
  expect(host.querySelector<HTMLInputElement>('input[value="reject"]')!.disabled).toBe(true);
  await choose("reject"); await submit();
  expect(decide.mock.calls).toEqual([["approve"], ["approve"]]);
  expect(host.querySelector<HTMLButtonElement>("button")!.disabled).toBe(true);
});

it("expires without polling or a rerender and cannot submit a choice after expiry", async () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-20T08:00:00Z"));
  const decide = vi.fn();
  await act(async () => root.render(<ComposerApproval language="en" summary="Edit file" details={null}
    expiresAt="2026-10-20T08:00:01Z" onDecide={decide} />));
  await choose("approve");
  await act(async () => { vi.advanceTimersByTime(1001); });
  await submit();
  expect(host.textContent).toContain("Approval expired");
  expect(decide).not.toHaveBeenCalled();
});

const approval = { id: "a", session_id: "first", status: "pending", summary: "Run the plan", source: "tool",
  arguments: { command: "echo hello" }, reason: "Approval required", resources: [], tool: "shell.exec",
  run_id: "run", tool_call_id: "call", risk: "dangerous", created_at: "" } satisfies Approval;

it("shows only the active session and pending requests; switching context cannot carry a selection", async () => {
  const decide = vi.fn();
  const render = (sessionID: string) => act(async () => root.render(<SessionComposerApprovals key={sessionID}
    approvals={[approval, { ...approval, id: "other", session_id: "second" }, { ...approval, id: "resolved", status: "approved" }]}
    sessionID={sessionID} text={dictionaries.en} language="en" enabled onDecide={decide} />));
  await render("first"); expect(host.querySelectorAll("section")).toHaveLength(1); await choose("approve");
  await render("second"); expect(host.querySelectorAll("input:checked")).toHaveLength(0); await choose("reject"); await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "other", session_id: "second" }), "reject");
});

it("keeps both choices disabled offline, and permits rejecting an unavailable Happy plan", async () => {
  const decide = vi.fn();
  const render = (enabled: boolean) => act(async () => root.render(<SessionComposerApprovals
    approvals={[{ ...approval, source: "happy_team_plan", external_context: { provider: "happy", title: "Plan", goal_prompt: "Goal", plan_availability: "temporarily_unavailable" } }]}
    sessionID="first" text={dictionaries.en} language="en" enabled={enabled} onDecide={decide} />));
  await render(false); await choose("reject"); await submit(); expect(decide).not.toHaveBeenCalled();
  await render(true);
  expect(host.querySelector<HTMLInputElement>('input[value="approve"]')!.disabled).toBe(true);
  await choose("reject"); await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "a" }), "reject");
});

it("disables unverified or unavailable local approvals and retires terminated task approvals", async () => {
  const decide = vi.fn();
  const task: LocalTask = { id: "t", request_id: "r", status: "running", created_at: "", approvals: [{
    approval_id: "a", digest: "d", state: "pending", tool: "browser.type", summary: "Type text", arguments: {},
    expires_at: new Date(Date.now() + 60000).toISOString(), actionable: true,
  }] };
  const render = (tasks: LocalTask[], enabled: boolean) => act(async () => root.render(<LocalComposerApprovals tasks={tasks} language="en" enabled={enabled} onDecide={decide} />));
  await render([task], false); await choose("approve"); await submit(); expect(decide).not.toHaveBeenCalled();
  await render([task], true); await choose("approve"); await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith(task, task.approvals![0], "approve");
  await render([{ ...task, termination_reason: "gateway_restart" }], true); expect(host.querySelector("section")).toBeNull();
});
