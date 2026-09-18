import { Fragment, useEffect, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { EmailMessage, EmailRenderNode, EmailRenderPreview as EmailRenderPreviewValue } from "../api/email";
import type { Copy } from "../i18n";

const embeddedImage = /^data:image\/(?:png|jpeg|webp);base64,/i;
const maxExternalURLLength = 8192;

function renderChildren(node: EmailRenderNode, path: string): ReactNode {
  return (node.children ?? []).map((child, index) => <EmailContentNode key={`${path}.${index}`} node={child} path={`${path}.${index}`} />);
}

function EmailHeading({ node, path }: { node: EmailRenderNode; path: string }) {
  const children = renderChildren(node, path);
  switch (node.level) {
    case 1: return <h2>{children}</h2>;
    case 3: return <h4>{children}</h4>;
    case 4: return <h5>{children}</h5>;
    case 5:
    case 6: return <h6>{children}</h6>;
    default: return <h3>{children}</h3>;
  }
}

function tableRows(node: EmailRenderNode): EmailRenderNode[] {
  const children = node.children ?? [];
  if (node.kind === "row") return [node];
  return children.flatMap(tableRows);
}

function isLayoutTable(node: EmailRenderNode) {
  const rows = tableRows(node);
  return rows.length > 0 && rows.every((row) => {
    const cells = (row.children ?? []).filter((child) => child.kind === "cell" || child.kind === "header_cell");
    return cells.length === 1 && cells[0].kind === "cell" && !cells[0].col_span && !cells[0].row_span;
  });
}

function EmailTable({ node, path }: { node: EmailRenderNode; path: string }) {
  if (isLayoutTable(node)) {
    return <div className="emailContentLayout">{tableRows(node).map((row, index) => {
      const cell = (row.children ?? []).find((child) => child.kind === "cell")!;
      return <div className="emailContentLayoutRow" key={`${path}.layout.${index}`}>{renderChildren(cell, `${path}.layout.${index}`)}</div>;
    })}</div>;
  }
  return <div className="emailContentTable"><table>{renderChildren(node, path)}</table></div>;
}

function safeExternalURL(value?: string) {
  if (!value || value.length > maxExternalURLLength) return null;
  try {
    const parsed = new URL(value);
    if ((parsed.protocol !== "https:" && parsed.protocol !== "http:") || !parsed.hostname || parsed.username || parsed.password) return null;
    return parsed;
  } catch {
    return null;
  }
}

function EmailExternalLink({ node, path }: { node: EmailRenderNode; path: string }) {
  const children = renderChildren(node, path);
  const target = safeExternalURL(node.url);
  if (!target) return <>{children}</>;
  return <a
    className="emailContentLink"
    href={target.href}
    target="_blank"
    rel="noopener noreferrer nofollow"
    referrerPolicy="no-referrer"
  >
    <span className="emailContentLinkLabel">{children}</span>
    <span className="emailContentLinkHost">{target.hostname} ↗</span>
  </a>;
}

function EmailContentNode({ node, path }: { node: EmailRenderNode; path: string }): ReactNode {
  const children = renderChildren(node, path);
  switch (node.kind) {
    case "text": return <Fragment>{node.text ?? ""}</Fragment>;
    case "section": return <div className="emailContentSection">{children}</div>;
    case "paragraph": return <p>{children}</p>;
    case "heading": return <EmailHeading node={node} path={path} />;
    case "strong": return <strong>{children}</strong>;
    case "emphasis": return <em>{children}</em>;
    case "underline": return <u>{children}</u>;
    case "strike": return <s>{children}</s>;
    case "small": return <small>{children}</small>;
    case "mark": return <mark>{children}</mark>;
    case "code": return <code>{children}</code>;
    case "preformatted": return <pre>{children}</pre>;
    case "quote": return <blockquote>{children}</blockquote>;
    case "list": return node.ordered ? <ol>{children}</ol> : <ul>{children}</ul>;
    case "item": return <li>{children}</li>;
    case "description_list": return <dl>{children}</dl>;
    case "term": return <dt>{children}</dt>;
    case "description": return <dd>{children}</dd>;
    case "table": return <EmailTable node={node} path={path} />;
    case "table_head": return <thead>{children}</thead>;
    case "table_body": return <tbody>{children}</tbody>;
    case "table_foot": return <tfoot>{children}</tfoot>;
    case "row": return <tr>{children}</tr>;
    case "header_cell": return <th colSpan={node.col_span} rowSpan={node.row_span}>{children}</th>;
    case "cell": return <td colSpan={node.col_span} rowSpan={node.row_span}>{children}</td>;
    case "image": return node.source && embeddedImage.test(node.source) ? <img src={node.source} alt={node.alt ?? ""} /> : null;
    case "link": return <EmailExternalLink node={node} path={path} />;
    case "line_break": return <br />;
    case "divider": return <hr />;
    default: return null;
  }
}

export function EmailRenderPreview({ mail, text }: { mail: EmailMessage; text: Copy }) {
  const [open, setOpen] = useState(false);
  const [preview, setPreview] = useState<EmailRenderPreviewValue | null>(null);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    if (!open || preview !== null) return;
    const controller = new AbortController();
    let active = true;
    setFailed(false);
    void api.emailRenderPreview(mail.id, controller.signal).then((value) => {
      if (active) setPreview(value);
    }).catch(() => {
      if (active) setFailed(true);
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [mail.id, open, preview, retry]);

  return <details className="emailRenderPreview" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>{text.email.safePreview}</summary>
    {open && (preview?.state === "ready" && preview.content.length > 0
      ? <div className="emailStructuredContent" aria-label={text.email.safePreviewTitle}>
          {preview.content.map((node, index) => <EmailContentNode key={index} node={node} path={String(index)} />)}
        </div>
      : preview !== null
        ? <p role="status">{text.email.previewUnavailable}</p>
        : failed
          ? <p role="alert">{text.email.loadFailed}<button className="emailTextButton" onClick={() => setRetry((value) => value + 1)}>{text.common.refresh}</button></p>
          : <p role="status">{text.email.loading}</p>)}
  </details>;
}
