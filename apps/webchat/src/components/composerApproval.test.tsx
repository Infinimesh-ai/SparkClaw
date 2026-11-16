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
import { supportedApprovalForm, type ApprovalInputForm } from "./approvalInputForm";

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
  await act(async () => root.render(<ComposerApproval text={dictionaries.zh} summary="发送这封邮件？" details={<pre>Recipient and body</pre>} lockOnAttempt onDecide={decide} />));
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

it("lets an unbound approval change its answer after a failed submission", async () => {
  const decide = vi.fn().mockRejectedValueOnce(new Error("Server error")).mockResolvedValue(undefined);
  await act(async () => root.render(<ComposerApproval text={dictionaries.en} summary="Run" details={null} onDecide={decide} />));
  await choose("approve"); await submit();
  expect(host.querySelector('[role="alert"]')?.textContent).toBe("Server error");
  expect(host.querySelector<HTMLInputElement>('input[value="reject"]')!.disabled).toBe(false);
  await choose("reject"); await submit();
  expect(decide.mock.calls).toEqual([["approve"], ["reject"]]);
  expect(host.querySelector('[role="status"]')?.textContent).toBe("Decision received.");
});

it("expires without polling or a rerender and cannot submit a choice after expiry", async () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-20T08:00:00Z"));
  const decide = vi.fn();
  await act(async () => root.render(<ComposerApproval text={dictionaries.en} summary="Edit file" details={null}
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
    sessionID={sessionID} text={dictionaries.en} enabled onDecide={decide} />));
  await render("first"); expect(host.querySelectorAll("section")).toHaveLength(1); await choose("approve");
  await render("second"); expect(host.querySelectorAll("input:checked")).toHaveLength(0); await choose("reject"); await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "other", session_id: "second" }), "reject");
});

it("keeps both choices disabled offline, and permits rejecting an unavailable Happy plan", async () => {
  const decide = vi.fn();
  const render = (enabled: boolean) => act(async () => root.render(<SessionComposerApprovals
    approvals={[{ ...approval, source: "happy_team_plan", external_context: { provider: "happy", title: "Plan", goal_prompt: "Goal", plan_availability: "temporarily_unavailable" } }]}
    sessionID="first" text={dictionaries.en} enabled={enabled} onDecide={decide} />));
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
  const render = (tasks: LocalTask[], enabled: boolean) => act(async () => root.render(<LocalComposerApprovals tasks={tasks} text={dictionaries.en} enabled={enabled} onDecide={decide} />));
  await render([task], false); await choose("approve"); await submit(); expect(decide).not.toHaveBeenCalled();
  await render([task], true); await choose("approve"); await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith(task, task.approvals![0], "approve");
  await render([{ ...task, termination_reason: "gateway_restart" }], true); expect(host.querySelector("section")).toBeNull();
});

it("describes workspace-data requests in the composer as the approval inbox does", async () => {
  const workspace = { ...approval, summary: "raw tool summary", reason: "raw reason", resources: ["/private/path"],
    presentation: { kind: "external_mcp_workspace_data_access", session_id: "first", requester: "AI", return_route: {}, scope: "single_operation" } } satisfies Approval;
  await act(async () => root.render(<SessionComposerApprovals approvals={[workspace]} sessionID="first" text={dictionaries.en} enabled onDecide={vi.fn()} />));
  expect(host.querySelector(".composerApprovalQuestion")?.textContent).toBe(dictionaries.en.approval.workspaceDataTitle);
  expect(host.textContent).toContain(dictionaries.en.approval.workspaceDataReason);
  expect(host.textContent).not.toContain("raw tool summary");
  expect(host.textContent).not.toContain("/private/path");
});

const completion = { schema_version: 1, entries: [
  { id: 'first', label: '布洛芬', summary: '24 粒，2027-05-31 到期', fields: [] },
  { id: 'second', label: '感冒灵', summary: '6 袋', fields: [{ name: 'expiry', label: '有效期', type: 'date_or_none' as const, required: true }] },
] };
it('collects missing batch fields once, preserves them offline, and freezes the original values on an uncertain submission', async () => {
  const decide = vi.fn().mockRejectedValueOnce(new Error('Receipt lost')).mockResolvedValue(undefined);
  const render = (offline = false) => act(async () => root.render(<ComposerApproval text={dictionaries.zh} summary="补全药品" details={null} lockOnAttempt
    inputForm={completion} unavailable={offline ? dictionaries.zh.approval.composer.offline : undefined} onDecide={decide} />));
  await render(); await choose('approve');
  expect(host.querySelector<HTMLButtonElement>('footer button')!.disabled).toBe(true);
  expect(host.querySelectorAll('.approvalInputForm fieldset')).toHaveLength(2);
  const date = host.querySelector<HTMLInputElement>('input[type="date"]')!;
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(date,'2028-01-31'); date.dispatchEvent(new Event('input',{bubbles:true})); });
  await render(true); expect(date.value).toBe('2028-01-31'); await submit(); expect(decide).not.toHaveBeenCalled();
  await render(false); expect(host.querySelector('footer button')!.textContent).toBe('补全并写入 2 条');
  await submit(); expect(decide).toHaveBeenCalledExactlyOnceWith('approve',{second:{expiry:'2028-01-31'}});
  expect(host.querySelector<HTMLFieldSetElement>('.approvalInputForm fieldset')!.disabled).toBe(true);
  await submit(); expect(decide.mock.calls).toEqual([['approve',{second:{expiry:'2028-01-31'}}],['approve',{second:{expiry:'2028-01-31'}}]]);
});
it('requires an explicit no-expiry choice and permits cancellation without filling fields', async () => {
  const decide=vi.fn();await act(async()=>root.render(<ComposerApproval text={dictionaries.en} summary="Medicine" details={null} inputForm={completion} onDecide={decide}/>));
  await choose('approve');expect(host.querySelector<HTMLButtonElement>('footer button')!.disabled).toBe(true);
  expect(host.querySelector('footer button')!.textContent).toBe('Complete and write 2 entries');
  await act(async()=>host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
  await submit();expect(decide).toHaveBeenCalledExactlyOnceWith('approve',{second:{expiry:'no_expiry'}});
  await act(async()=>root.render(<ComposerApproval key="cancel" text={dictionaries.en} summary="Medicine" details={null} inputForm={completion} onDecide={decide}/>));
  await choose('reject');await submit();expect(decide).toHaveBeenLastCalledWith('reject');
});

it.each([
  { language: 'zh' as const, label: '确认点餐信息' },
  { language: 'en' as const, label: 'Confirm order details' },
])('uses the $language form submit label while retaining selection, validation and locked retries', async ({ language, label }) => {
  const text = dictionaries[language];
  const copy = text.approval.composer;
  const decide = vi.fn().mockRejectedValueOnce(new Error('Receipt lost')).mockResolvedValue(undefined);
  const form: ApprovalInputForm = { schema_version: 1, submit_label: label, entries: [{
    id: 'store', label: 'Store', summary: 'Choose a store', fields: [{
      name: 'store', label: 'Store', type: 'select', required: true, options: [{ value: 'store_1', label: 'First store' }],
    }],
  }] };
  await act(async () => root.render(<ComposerApproval text={text} summary="Order" details={<p>Order details</p>}
    inputForm={form} lockOnAttempt onDecide={decide} />));
  expect(host.querySelector('footer button')!.textContent).toBe(copy.submit);
  expect(host.querySelector('details summary')!.textContent).toBe(copy.viewDetails);
  expect(host.querySelectorAll('input[type="radio"]')).toHaveLength(2);
  await choose('reject');
  expect(host.querySelector('footer button')!.textContent).toBe(copy.submit);
  await choose('approve');
  expect(host.querySelector('footer button')!.textContent).toBe(label);
  expect(host.querySelector<HTMLButtonElement>('footer button')!.disabled).toBe(true);
  const select = host.querySelector<HTMLSelectElement>('select')!;
  await act(async () => { select.value = 'store_1'; select.dispatchEvent(new Event('change', { bubbles: true })); });
  expect(host.querySelector<HTMLButtonElement>('footer button')!.disabled).toBe(false);
  expect(decide).not.toHaveBeenCalled();
  await submit();
  expect(decide).toHaveBeenCalledExactlyOnceWith('approve', { store: { store: 'store_1' } });
  expect(host.querySelector('footer button')!.textContent).toBe(copy.retry);
  expect(host.querySelector<HTMLFieldSetElement>('.approvalInputForm fieldset')!.disabled).toBe(true);
  expect(host.querySelector<HTMLInputElement>('input[value="reject"]')!.disabled).toBe(true);
  await submit();
  expect(decide.mock.calls).toEqual([['approve', { store: { store: 'store_1' } }], ['approve', { store: { store: 'store_1' } }]]);
  expect(host.querySelector('[role="status"]')!.textContent).toBe(copy.received);
});

it.each(['zh', 'en'] as const)('keeps the %s medicine-box button label when submit_label is empty', async (language) => {
  await act(async () => root.render(<ComposerApproval text={dictionaries[language]} summary="Medicine" details={null}
    inputForm={{ ...completion, submit_label: '' }} onDecide={vi.fn()} />));
  await choose('approve');
  expect(host.querySelector('footer button')!.textContent).toBe(dictionaries[language].approval.composer.completeAndWrite.replace('{count}', '2'));
});

it.each([' ', '确'.repeat(81), 'Confirm\norder', 'Confirm\torder', 'Confirm\u007forder', 'Confirm\u0085order', 'Confirm\u2028order', 'Confirm\u2029order', '\ud800', 12, null])(
  'rejects an invalid form submit label: %j', (label) => {
    expect(supportedApprovalForm({ ...completion, submit_label: label } as ApprovalInputForm)).toBe(false);
  },
);

it('counts Unicode code points in submit labels', () => {
  expect(supportedApprovalForm({ ...completion, submit_label: '确'.repeat(80) })).toBe(true);
  expect(supportedApprovalForm({ ...completion, submit_label: '🍟'.repeat(80) })).toBe(true);
  expect(supportedApprovalForm({ ...completion, submit_label: '🍟'.repeat(81) })).toBe(false);
});
it('retains the approval entry but disables unsupported completion form versions', async()=>{
 const decide=vi.fn();await act(async()=>root.render(<ComposerApproval text={dictionaries.en} summary="Medicine" details={null} inputForm={{...completion,schema_version:2}} onDecide={decide}/>));
 expect(host.querySelector<HTMLInputElement>('input[value="approve"]')!.disabled).toBe(true);
 expect(host.textContent).toContain('client that supports');await choose('reject');await submit();expect(decide).toHaveBeenCalledExactlyOnceWith('reject');
});
