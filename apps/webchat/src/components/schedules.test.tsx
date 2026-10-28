// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { ScheduleCreateDialog, ScheduleSuggestions } from "./schedules";

describe("ScheduleCreateDialog", () => {
  const roots: ReturnType<typeof createRoot>[] = [];
  const hosts: HTMLElement[] = [];

  afterEach(() => {
    for (const root of roots.splice(0)) act(() => root.unmount());
    for (const host of hosts.splice(0)) host.remove();
  });

  function render(onCreate: (request: string) => Promise<{ message: string; success: boolean } | null>) {
    const host = document.createElement("div");
    document.body.append(host);
    hosts.push(host);
    const root = createRoot(host);
    const onClose = vi.fn();
    roots.push(root);
    act(() => root.render(<ScheduleCreateDialog busy={false} text={dictionaries.zh} onClose={onClose} onCreate={onCreate} />));
    return { host, onClose };
  }

  async function enter(textarea: HTMLTextAreaElement, value: string) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, value);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("submits the requirement and closes after creation", async () => {
    const onCreate = vi.fn(async () => ({ message: "已创建", success: true }));
    const { host, onClose } = render(onCreate);
    const textarea = host.querySelector("textarea")!;

    await enter(textarea, " 每个工作日早上九点生成进展摘要 ");
    await act(async () => host.querySelector("form")!.requestSubmit());

    expect(onCreate).toHaveBeenCalledWith("每个工作日早上九点生成进展摘要");
    expect(textarea.value).toBe("");
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("keeps the requirement editable when the task needs more detail", async () => {
    const onCreate = vi.fn(async () => ({ message: "请补充执行时间", success: false }));
    const { host, onClose } = render(onCreate);
    const textarea = host.querySelector("textarea")!;

    await enter(textarea, "整理进展");
    await act(async () => host.querySelector("form")!.requestSubmit());

    expect(textarea.value).toBe("整理进展");
    expect(host.textContent).toContain("请补充执行时间");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("closes on Escape without submitting", async () => {
    const onCreate = vi.fn(async () => ({ message: "已创建", success: true }));
    const { host, onClose } = render(onCreate);

    await act(async () => host.querySelector('[role="dialog"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));

    expect(onClose).toHaveBeenCalledOnce();
    expect(onCreate).not.toHaveBeenCalled();
  });
});

describe("ScheduleSuggestions", () => {
  it("offers example requests that open the create dialog filled in", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), choose = vi.fn();
    try {
      await act(async () => root.render(<ScheduleSuggestions text={dictionaries.zh} disabled={false} onChoose={choose} />));
      const suggestions = host.querySelectorAll<HTMLButtonElement>(".scheduleSuggestion");
      expect(suggestions).toHaveLength(dictionaries.zh.schedules.suggestions.length);
      await act(async () => suggestions[1].click());
      expect(choose).toHaveBeenCalledExactlyOnceWith(dictionaries.zh.schedules.suggestions[1]);
      await act(async () => root.render(<ScheduleCreateDialog busy={false} text={dictionaries.zh} initialRequest={dictionaries.zh.schedules.suggestions[1]} onClose={() => {}} onCreate={async () => null} />));
      expect(host.querySelector("textarea")!.value).toBe(dictionaries.zh.schedules.suggestions[1]);
    } finally { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); }
  });
});

describe("ScheduleBar offline occurrences", () => {
  it("shows permanent skips and only advertises a pending future occurrence as next", async () => {
    const { renderToStaticMarkup } = await import("react-dom/server");
    const { ScheduleBar } = await import("./schedules");
    const base = {
      text: "Scheduled request", timezone: "UTC", updated_at: "2026-10-06T10:00:00Z",
      editable: false, cancelable: false, endpoint: { status: "not_applicable" as const },
    };
    const html = renderToStaticMarkup(<ScheduleBar
      schedules={[
        { ...base, id: "missed", title: "Skipped task", status: "missed", due_time: "2026-10-06T09:00:00Z" },
        { ...base, id: "submitted", title: "Original request", status: "submitted", due_time: "2026-10-06T10:00:00Z" },
      ]}
      open loading={false} busyId="" language="en" text={dictionaries.en}
      onToggle={() => {}} onRefresh={() => {}} onEdit={async () => {}} onDelete={async () => {}}
    />);
    expect(html).toContain("Skipped while offline");
    expect(html).toContain("Submitted");
    expect(html).toContain("No current scheduled tasks.");
    expect(html).not.toContain(">Next ");
  });
});
