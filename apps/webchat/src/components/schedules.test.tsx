// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { ScheduleCreateDialog } from "./schedules";

describe("ScheduleCreateDialog", () => {
  const roots: ReturnType<typeof createRoot>[] = [];

  afterEach(() => {
    for (const root of roots.splice(0)) act(() => root.unmount());
  });

  function render(onCreate: (request: string) => Promise<{ message: string; success: boolean } | null>) {
    const host = document.createElement("div");
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
