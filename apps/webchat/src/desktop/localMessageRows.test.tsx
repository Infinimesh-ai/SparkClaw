// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { MessageBubble } from "../components/messages";
import { dictionaries } from "../i18n";
import type { LocalConversationContent } from "./clientStore";
import { ConversationReadVersion } from "./conversationReadVersion";
import { localMessageRows } from "./localMessageRows";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const container = document.createElement("div"); document.body.append(container);
const root = createRoot(container);
afterEach(async () => { await act(async () => root.render(null)); });

const preview: LocalConversationContent = { messages: [], files: [], tasks: [{ id: "task", request_id: "request", status: "running", explicitly_submitted: 1, created_at: "", progress_content: "前六个字符" }] };
const final: LocalConversationContent = { messages: [{ id: "durable-message", request_id: "request", role: "assistant", content: "前六个字符，权威完成。", created_at: "" }], files: [], tasks: [{ ...preview.tasks[0], status: "delivered" }] };
function Rows({ content }: { content: LocalConversationContent }) {
  return localMessageRows(content, "conversation").map(row => <MessageBubble key={row.key} message={row.message}
    scrollAnchorId={row.scrollAnchorId} inProgress={row.inProgress} streamStatuses={[]} text={dictionaries.zh} language="zh" onFeedback={async () => {}} />);
}

describe("execution answer projection in original messages", () => {
  it.each(["前六个字符", "前六个字符，权威完成。", "权威修正全文。"])('promotes %s in the same article without a duplicate or animation', async content => {
    await act(async () => root.render(<Rows content={preview} />));
    const article = container.querySelector("article")!;
    expect(article.textContent).toContain(preview.tasks[0].progress_content);
    expect(article.dataset.scrollAnchor).toBe("execution:request");
    await act(async () => root.render(<Rows content={{ ...final, messages: [{ ...final.messages[0], content }] }} />));
    expect(container.querySelectorAll("article")).toHaveLength(1);
    expect(container.querySelector("article")).toBe(article);
    expect(article.dataset.messageId).toBe("durable-message");
    expect(article.dataset.scrollAnchor).toBe("execution:request");
    expect(article.textContent).toContain(content);
    expect(article.textContent).not.toContain(dictionaries.zh.chat.waiting);
  });
  it("rejects a late preview read after a newer final transaction read", async () => {
    const reads = new ConversationReadVersion();
    let release!: (value: LocalConversationContent) => void;
    const old = new Promise<LocalConversationContent>(resolve => { release = resolve; });
    const commit = (content: LocalConversationContent) => root.render(<Rows content={content} />);
    await act(async () => commit(preview));
    const article = container.querySelector("article");
    const pending = reads.read(() => old, () => true, commit);
    await act(async () => { await reads.read(async () => final, () => true, commit); });
    await act(async () => { release(preview); await pending; });
    expect(container.querySelector("article")).toBe(article);
    expect(container.textContent).toContain("权威完成。");
    expect(container.querySelectorAll("article")).toHaveLength(1);
  });
  it("does not duplicate final text if a stale task still says running in the same view", () => {
    expect(localMessageRows({ ...final, tasks: preview.tasks }, "conversation")).toHaveLength(1);
  });
  it("publishes a slow read while a newer read is still pending, then advances", async () => {
    const reads = new ConversationReadVersion();
    const values: number[] = [];
    const release: Array<(value: number) => void> = [];
    const first = reads.read(() => new Promise<number>(resolve => release.push(resolve)), () => true, value => values.push(value));
    const second = reads.read(() => new Promise<number>(resolve => release.push(resolve)), () => true, value => values.push(value));
    release[0](1); await first;
    expect(values).toEqual([1]);
    release[1](2); await second;
    expect(values).toEqual([1, 2]);
  });
  it("does not let a stale conversation read invalidate the newly selected conversation", async () => {
    const reads = new ConversationReadVersion();
    let selected = "a";
    const values: string[] = [];
    let release!: (value: string) => void;
    const old = reads.read(() => new Promise<string>(resolve => { release = resolve; }), () => selected === "a", value => values.push(value));
    selected = "b";
    await reads.read(async () => "b", () => selected === "b", value => values.push(value));
    await reads.read(async () => "late a", () => selected === "a", value => values.push(value));
    release("a"); await old;
    expect(values).toEqual(["b"]);
  });
});
