// Chat message rendering: bubbles, attachments, inline markdown-ish text,
// workspace images/screenshots, and stream status lines.
import { Fragment, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Bot, Check, Download, FileSearch, ThumbsDown, ThumbsUp, UserRound } from "lucide-react";
import { fetchAuthedBlob, fetchDocumentFile, openDocumentFile, workspaceScreenshotURL } from "../api/client";
import { cssToken, formatTime } from "../lib/format";
import { MESSAGE_STREAM_STARTED_EVENT } from "../lib/messageStream";
import type { Message, MessageAttachment } from "../api/types";
import type { Copy, Language } from "../i18n";

export type StreamStatus = {
  id: string;
  type: string;
  text: string;
};

export function MessageBubble({
  message,
  streamStatuses,
  text,
  language,
  onFeedback
}: {
  message: Message;
  streamStatuses: StreamStatus[];
  text: Copy;
  language: Language;
  onFeedback: (rating: "up" | "down" | "corrected", correction?: string) => Promise<void>;
}) {
  const [correction, setCorrection] = useState("");
  const [saving, setSaving] = useState(false);

  async function submit(rating: "up" | "down" | "corrected") {
    if (saving || !message.run_id) return;
    setSaving(true);
    try {
      await onFeedback(rating, rating === "corrected" ? correction.trim() : "");
      if (rating === "corrected") setCorrection("");
    } catch {
      return;
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className={`message ${message.role}`}>
      <div className="messageMeta">
        <span>{message.role === "user" ? text.chat.you : text.chat.assistant}</span>
        <time>{formatTime(message.created_at, language)}</time>
      </div>
      {message.attachments && message.attachments.length > 0 && (
        <MessageAttachments attachments={message.attachments} sessionId={message.session_id} text={text} inlineOutputs={message.role === "assistant"} />
      )}
      {streamStatuses.length > 0 && <StreamStatusList statuses={streamStatuses} />}
      {message.content.trim() && <MessageContent content={message.content} sessionId={message.session_id} text={text} />}
      {message.role === "assistant" && message.run_id && (
        <div className="feedbackBar">
          <button onClick={() => void submit("up")} disabled={saving} title={text.chat.helpful}>
            <ThumbsUp size={14} />
          </button>
          <button onClick={() => void submit("down")} disabled={saving} title={text.chat.unhelpful}>
            <ThumbsDown size={14} />
          </button>
          <input
            aria-label={text.chat.correction}
            value={correction}
            onChange={(event) => setCorrection(event.target.value)}
            disabled={saving}
            placeholder={text.chat.correction}
          />
          <button onClick={() => void submit("corrected")} disabled={saving || !correction.trim()} title={text.chat.saveCorrection}>
            <Check size={14} />
          </button>
        </div>
      )}
    </article>
  );
}

export function MessageAttachments({
  attachments,
  sessionId,
  text,
  inlineOutputs = false
}: {
  attachments: MessageAttachment[];
  sessionId: string;
  text: Copy;
  inlineOutputs?: boolean;
}) {
  const inlineImages = inlineOutputs ? attachments.filter(isImageAttachment) : [];
  const outputFiles = inlineOutputs ? attachments.filter((attachment) => !isImageAttachment(attachment)) : [];
  const ordinaryAttachments = inlineOutputs ? [] : attachments;
  return (
    <>
      {inlineImages.map((attachment) => (
        <WorkspaceMediaImage
          key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}
          path={attachment.rel_path}
          sessionId={sessionId}
          alt={attachment.caption || attachment.name || attachment.rel_path}
        />
      ))}
      {outputFiles.map((attachment) => (
        <WorkspaceDocumentResult
          key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}
          path={attachment.rel_path}
          sessionId={sessionId}
          label={text.chat.modifiedFile}
          text={text}
        />
      ))}
      {ordinaryAttachments.length > 0 && (
        <div className="messageAttachments">
          {ordinaryAttachments.map((attachment) => (
            isImageAttachment(attachment) ? (
              <button
                key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}
                className="messageAttachment image"
                type="button"
                title={text.chat.openAttachment}
                onClick={() => void openDocumentFile(attachment.rel_path, sessionId).catch(() => undefined)}
              >
                <WorkspaceFileImage path={attachment.rel_path} sessionId={sessionId} alt={attachment.name || attachment.rel_path} />
                <span>{attachment.name || attachment.rel_path}</span>
              </button>
            ) : (
              <button
                key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}
                className="messageAttachment"
                type="button"
                title={text.chat.openAttachment}
                onClick={() => void openDocumentFile(attachment.rel_path, sessionId).catch(() => undefined)}
              >
                <FileSearch size={15} />
                <span>{attachment.name || attachment.rel_path}</span>
              </button>
            )
          ))}
        </div>
      )}
    </>
  );
}

export function isImageContentType(contentType?: string) {
  return (contentType || "").toLowerCase().startsWith("image/");
}

export function isImageAttachment(attachment: MessageAttachment) {
  if (isImageContentType(attachment.content_type)) return true;
  return attachment.rel_path.startsWith("media/");
}

export function StreamStatusList({ statuses }: { statuses: StreamStatus[] }) {
  if (statuses.length === 0) return null;
  return (
    <div className="streamStatusList">
      {statuses.map((status) => (
        <span key={status.id} className={`streamStatus ${cssToken(status.type)}`}>
          {status.text}
        </span>
      ))}
    </div>
  );
}

export function streamStatusFromEvent(event: string, data: unknown, text: Copy): StreamStatus | null {
  if (event === MESSAGE_STREAM_STARTED_EVENT) {
    return { id: "waiting", type: "waiting", text: text.chat.waiting };
  }
  const payload = streamPayload(data);
  if (event.startsWith("tool_call.")) {
    const tool = stringField(payload, "tool");
    const label = tool ? `：${tool}` : "";
    if (event === "tool_call.started") {
      return { id: `tool:${tool || "unknown"}`, type: "tool_started", text: `${text.chat.toolStarted}${label}` };
    }
    if (event === "tool_call.completed" || event === "tool_call.completed_after_approval") {
      return { id: `tool:${tool || "unknown"}`, type: "tool_completed", text: `${text.chat.toolCompleted}${label}` };
    }
    if (event === "tool_call.failed" || event === "tool_call.failed_after_approval" || event === "tool_call.blocked") {
      return { id: `tool:${tool || "unknown"}`, type: "tool_failed", text: `${text.chat.toolFailed}${label}` };
    }
    if (event === "tool_call.approval_pending") {
      return { id: `approval:${tool || "unknown"}`, type: "approval_pending", text: `${text.chat.approvalPending}${label}` };
    }
  }
  if (event.startsWith("approval.")) {
    const tool = stringField(payload, "tool");
    const label = tool ? `：${tool}` : "";
    if (event === "approval.pending") {
      return { id: `approval:${tool || "unknown"}`, type: "approval_pending", text: `${text.chat.approvalPending}${label}` };
    }
    if (event === "approval.approved") {
      return { id: `approval:${tool || "unknown"}`, type: "approval_approved", text: `${text.chat.approvalApproved}${label}` };
    }
    if (event === "approval.rejected") {
      return { id: `approval:${tool || "unknown"}`, type: "approval_rejected", text: `${text.chat.approvalRejected}${label}` };
    }
  }
  return null;
}

export function streamPayload(data: unknown): Record<string, unknown> {
  if (!data || typeof data !== "object") return {};
  if ("payload" in data && data.payload && typeof data.payload === "object") {
    return data.payload as Record<string, unknown>;
  }
  return data as Record<string, unknown>;
}

export function stringField(value: Record<string, unknown>, key: string) {
  const field = value[key];
  return typeof field === "string" ? field : "";
}

export function upsertStreamStatus(statuses: StreamStatus[], next: StreamStatus) {
  const filtered = statuses.filter((status) => status.id !== next.id && !(next.type !== "waiting" && status.id === "waiting"));
  return [...filtered, next].slice(-5);
}

export function MessageContent({ content, sessionId, text }: { content: string; sessionId: string; text: Copy }) {
  const documentResult = parseDocumentResultContent(content);
  if (documentResult) return <WorkspaceDocumentResult path={documentResult.path} sessionId={sessionId} label={documentResult.label || text.chat.modifiedFile} text={text} />;
  const mediaImage = parseSingleMediaImageContent(content);
  if (mediaImage) return <WorkspaceMediaImage path={mediaImage.path} sessionId={sessionId} alt={mediaImage.alt} />;
  const screenshot = parseScreenshotContent(content);
  if (!screenshot) return <RenderedMessageText content={content} />;
  return (
    <div className="messageContent">
      {screenshot.text ? <RenderedMessageText content={screenshot.text} /> : null}
      <WorkspaceScreenshot path={screenshot.path} />
      <p>截图已保存到：{screenshot.path}</p>
    </div>
  );
}

export function WorkspaceDocumentResult({ path, sessionId, label, text }: { path: string; sessionId: string; label: string; text: Copy }) {
  const fileName = path.split("/").pop() || path;
  return (
    <div className="messageContent">
      <button
        className="messageDocumentResult"
        type="button"
        onClick={() => void openDocumentFile(path, sessionId).catch(() => undefined)}
        title={text.chat.openFile}
      >
        <FileSearch size={18} />
        <span>
          <strong>{label}</strong>
          <small>{fileName}</small>
        </span>
        <Download size={15} />
      </button>
    </div>
  );
}

export function WorkspaceMediaImage({ path, sessionId, alt }: { path: string; sessionId: string; alt: string }) {
  return (
    <div className="messageContent mediaOnly">
      <button
        className="messageMediaImageButton"
        type="button"
        onClick={() => void openDocumentFile(path, sessionId).catch(() => undefined)}
        title={alt || path}
      >
        <WorkspaceFileImage className="messageMediaImage" path={path} sessionId={sessionId} alt={alt || "media image"} />
      </button>
    </div>
  );
}

export function WorkspaceFileImage({ path, sessionId, alt, className = "" }: { path: string; sessionId: string; alt: string; className?: string }) {
  const [src, setSrc] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    let objectURL = "";
    fetchDocumentFile(path, sessionId, controller.signal)
      .then((blob) => {
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setSrc(objectURL);
      })
      .catch(() => {
        if (!controller.signal.aborted) setSrc("");
      });
    return () => {
      cancelled = true;
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [path, sessionId]);

  if (!src) return <span className={`workspaceImagePlaceholder ${className}`} aria-hidden="true" />;
  return <img className={className || undefined} src={src} alt={alt} />;
}

export function parseSingleMediaImageContent(content: string): { alt: string; path: string } | null {
  const trimmed = content.trim();
  const match = trimmed.match(/^!\[([^\]]*)\]\(([^)]+)\)$/);
  if (!match) return null;
  const rawPath = normalizeWorkspaceMediaPath(match[2].trim());
  if (!rawPath) return null;
  return { alt: match[1].trim(), path: rawPath };
}

export function normalizeWorkspaceMediaPath(path: string) {
  const clean = path.replace(/^workspace:\/\//, "").replace(/^\/+/, "");
  if (!clean.startsWith("media/")) return "";
  if (!/\.(png|jpe?g|gif|webp)$/i.test(clean)) return "";
  return clean;
}

export function parseDocumentResultContent(content: string): { label: string; path: string } | null {
  const trimmed = content.trim();
  const match = trimmed.match(/^(修改好的文件|输出文件|Modified file|Output file)[：:]\s*(?:workspace:\/\/)?((?:outputs|uploads)\/[^\s]+\.(?:docx|xlsx|pptx|pdf|txt|md|csv|tsv))$/i);
  if (!match) return null;
  const path = normalizeWorkspaceDocumentPath(match[2]);
  if (!path) return null;
  return { label: match[1], path };
}

export function normalizeWorkspaceDocumentPath(path: string) {
  const clean = path.replace(/^workspace:\/\//, "").replace(/^\/+/, "");
  if (!/^(outputs|uploads)\//.test(clean)) return "";
  if (!/\.(docx|xlsx|pptx|pdf|txt|md|csv|tsv)$/i.test(clean)) return "";
  if (clean.includes("..")) return "";
  return clean;
}

export function RenderedMessageText({ content }: { content: string }) {
  const blocks = parseMessageBlocks(content);
  return (
    <div className="messageContent">
      {blocks.map((block, index) => {
        if (block.type === "heading") {
          return (
            <p key={index} className={`messageHeading level${block.level}`}>
              {renderInlineMessageText(block.text, index)}
            </p>
          );
        }
        if (block.type === "list") {
          return (
            <ul key={index} className="messageList">
              {block.items.map((item, itemIndex) => (
                <li key={itemIndex}>{renderInlineMessageText(item, itemIndex)}</li>
              ))}
            </ul>
          );
        }
        return <p key={index}>{renderInlineMessageText(block.text, index)}</p>;
      })}
    </div>
  );
}

export function WorkspaceScreenshot({ path }: { path: string }) {
  const [src, setSrc] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";
    fetchAuthedBlob(workspaceScreenshotURL(path))
      .then((blob) => {
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setSrc(objectURL);
      })
      .catch(() => {
        if (!cancelled) setSrc("");
      });
    return () => {
      cancelled = true;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [path]);

  if (!src) return null;
  return <img className="messageScreenshot" src={src} alt="browser screenshot" />;
}

export function parseScreenshotContent(content: string): { text: string; path: string } | null {
  const markdown = content.match(/!\[[^\]]*\]\(([^)]+?\.(?:png|jpe?g))\)/i);
  const saved = content.match(/截图已保存到：\s*([^\n]+?\.(?:png|jpe?g))/i);
  const path = (saved?.[1] ?? markdown?.[1] ?? "").trim();
  if (!path || !path.includes("/.sparkclaw/screenshots/")) return null;
  const text = content
    .replace(/!\[[^\]]*\]\([^)]+\)/g, "")
    .replace(/截图已保存到：\s*[^\n]+/g, "")
    .trim();
  return { text, path };
}

export type MessageBlock =
  | { type: "paragraph"; text: string }
  | { type: "heading"; level: 1 | 2 | 3; text: string }
  | { type: "list"; items: string[] };

export function parseMessageBlocks(content: string): MessageBlock[] {
  const blocks: MessageBlock[] = [];
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  let paragraph: string[] = [];
  let listItems: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    blocks.push({ type: "paragraph", text: paragraph.join("\n").trim() });
    paragraph = [];
  };
  const flushList = () => {
    if (listItems.length === 0) return;
    blocks.push({ type: "list", items: listItems });
    listItems = [];
  };

  for (const rawLine of lines) {
    const line = rawLine.trimEnd();
    if (!line.trim()) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ type: "heading", level: heading[1].length as 1 | 2 | 3, text: heading[2].trim() });
      continue;
    }
    const bullet = line.match(/^\s*(?:[-*+]|\d+[.)])\s+(.+)$/);
    if (bullet) {
      flushParagraph();
      listItems.push(bullet[1].trim());
      continue;
    }
    flushList();
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  return blocks.length > 0 ? blocks : [{ type: "paragraph", text: content }];
}

export function renderInlineMessageText(text: string, keyPrefix: number) {
  const nodes: ReactNode[] = [];
  const pattern = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  let part = 0;
  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(renderPlainMessageText(text.slice(lastIndex, match.index), `${keyPrefix}-${part++}`));
    }
    const token = match[0];
    if (token.startsWith("**")) {
      nodes.push(
        <strong key={`${keyPrefix}-${part++}`} className="messageStrong">
          {renderPlainMessageText(token.slice(2, -2), `${keyPrefix}-${part++}`)}
        </strong>
      );
    } else {
      nodes.push(
        <code key={`${keyPrefix}-${part++}`} className="messageCode">
          {token.slice(1, -1)}
        </code>
      );
    }
    lastIndex = match.index + token.length;
  }
  if (lastIndex < text.length) {
    nodes.push(renderPlainMessageText(text.slice(lastIndex), `${keyPrefix}-${part++}`));
  }
  return nodes.length > 0 ? nodes : text;
}

export function renderPlainMessageText(text: string, keyPrefix: string) {
  const parts = text.split("\n");
  if (parts.length === 1) return <Fragment key={keyPrefix}>{text}</Fragment>;
  return parts.map((part, index) => (
    <Fragment key={`${keyPrefix}-${index}`}>
      {index > 0 ? <br /> : null}
      {part}
    </Fragment>
  ));
}
