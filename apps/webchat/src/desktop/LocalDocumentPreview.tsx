import { useEffect, useRef, useState } from "react";
import { localFileError, type LocalFile, type LocalFilesBridge, type LocalPreview, type ReadingPosition } from "./localFiles";
import type { PreviewDocument, PreviewRequest, PreviewResponse } from "../local-preview/channel";

const PREVIEW_ORIGIN = "sparkclaw-preview://viewer";
const MAX_READ = 1024 * 1024;
type Props = { files: LocalFilesBridge; file: LocalFile; loginGeneration: string; zh: boolean; position?: ReadingPosition; onPosition: (position: ReadingPosition) => void; onInvalid: () => void; onEscape?: () => void };
const invalidSource = new Set(["source_changed", "locked", "not_found", "permission_denied", "revoked", "unavailable", "reference_expired", "cloud_placeholder", "remote_location"]);
export function previewFormat(name: string): PreviewDocument['format'] | 'office' | 'xlsx' | 'csv' | 'tsv' {
  const extension = name.split(".").at(-1)?.toLowerCase() || "";
  if (["doc", "docx", "ppt", "pptx"].includes(extension)) return "office";
  if (extension === "xlsx" || extension === "csv" || extension === "tsv") return extension;
  if (extension === "pdf") return "pdf";
  if (["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "avif"].includes(extension)) return "image";
  if (["md", "markdown"].includes(extension)) return "markdown";
  if (["xls", "zip", "gz", "dmg", "exe", "mp4", "mp3", "sqlite", "db"].includes(extension)) return "unsupported";
  return "text";
}

// The workbench mediates a channel for exactly this source version. The viewer
// never receives a local reference, locator, absolute path or business bridge.
export function LocalDocumentPreview({ files, file, loginGeneration, zh, position, onPosition, onInvalid, onEscape }: Props) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [error, setError] = useState("");
  const [instance, setInstance] = useState(0);
  const callbacks = useRef({ onPosition, onInvalid, onEscape }); callbacks.current = { onPosition, onInvalid, onEscape };
  const initialPosition = useRef(position);
  useEffect(() => {
    const target = frame.current;
    if (!target) return;
    let active = true, initializing = false, loaded = false, port: MessagePort | undefined, inflight = 0, preview: Promise<LocalPreview> | undefined;
    const documentId = crypto.randomUUID(), generation = crypto.getRandomValues(new Uint32Array(1))[0], readerId = crypto.randomUUID();
    const format = previewFormat(file.name);
    const requests = new Map<PreviewRequest['id'], string>();
    const revoke = () => {
      active = false; port?.postMessage({ type: "revoke" }); port?.close(); port = undefined;
      for (const id of requests.values()) void files.cancel(id).catch(() => {});
      void files.cancelPreview(readerId).catch(() => {}); void files.releasePreview(readerId).catch(() => {});
    };
    const obtainPreview = () => {
      if (!["office", "xlsx", "csv", "tsv"].includes(format)) throw new Error("Unsupported preview request");
      return preview ??= files.preview({ ref: file.ref, version: file.version, kind: format as "office" | "xlsx" | "csv" | "tsv", readerId });
    };
    const ready = async (event: MessageEvent) => {
      if (!active || event.source !== target.contentWindow || event.origin !== PREVIEW_ORIGIN ||
          event.data?.type !== "sparkclaw-preview:ready" || event.data?.protocol !== 1 || initializing) return;
      if (port) { revoke(); setError(zh ? "预览已重新载入，请重新打开。" : "The preview reloaded. Reopen it to continue."); return; }
      initializing = true;
      try {
        const latest = await files.stat(file.ref);
        const converted = ["office", "xlsx", "csv", "tsv"].includes(format) ? await obtainPreview() : undefined;
        if (!active) return;
        if (latest.version !== file.version) throw Object.assign(new Error("Source changed"), { code: "source_changed" });
        const channel = new MessageChannel(); port = channel.port1;
        port.onmessage = async ({ data }) => {
          if (!active || !data || data.documentId !== documentId || data.generation !== generation || data.loginGeneration !== loginGeneration) return;
          if (data.type === "escape") { callbacks.current.onEscape?.(); return; }
          if (data.type === "position") {
            const value = data.position;
            if (value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length <= 5 && Object.entries(value).every(([key, n]) => key === "sheet" ? typeof n === "string" && n.length <= 100 :
              ["page", "zoom", "scrollTop", "scrollLeft", "offset"].includes(key) && typeof n === "number" && Number.isFinite(n) && n >= 0 && n <= 10_000_000)) callbacks.current.onPosition(value);
            return;
          }
          if (data.type !== "request" || !Number.isSafeInteger(data.id) || data.id < 1 || requests.has(data.id) || inflight >= 4) return;
          const id: PreviewRequest['id'] = data.id;
          const requestId = crypto.randomUUID(); requests.set(id, requestId); inflight++;
          try {
            let value: unknown;
            if (data.operation === "read") {
              if (!Number.isSafeInteger(data.offset) || data.offset < 0 || !Number.isSafeInteger(data.length) || data.length < 1 || data.length > MAX_READ || data.offset + data.length > (converted?.size ?? file.size)) throw new Error("Invalid read range");
              if (format === "office") {
                const converted = await obtainPreview();
                value = await files.readPreview({ previewId: converted.previewId, readerId, offset: data.offset, length: data.length });
              } else value = await files.readBytes({ ref: file.ref, version: file.version, offset: data.offset, length: data.length, request_id: requestId });
            } else if (data.operation === "model") value = (await obtainPreview()).model;
            else if (data.operation === "image" && format === "markdown" && typeof data.relativePath === "string" && data.relativePath.length <= 2048) {
              value = await files.relativeImage({ ref: file.ref, version: file.version, path: data.relativePath });
            } else throw new Error("Unsupported preview operation");
            // A queued read cannot outlive sign-out, source replacement or tab close.
            if (active) port?.postMessage({ type: "response", id, documentId, generation, loginGeneration, ok: true, value } satisfies PreviewResponse);
          } catch (reason) {
            const code = (reason as { code?: string })?.code;
            if (active) port?.postMessage({ type: "response", id, documentId, generation, loginGeneration, ok: false, error: { code: code || "unavailable", message: localFileError(reason, zh) } } satisfies PreviewResponse);
            if (invalidSource.has(code || "")) { callbacks.current.onInvalid(); revoke(); }
          } finally { requests.delete(id); inflight--; }
        };
        port.start();
        target.contentWindow?.postMessage({ type: "sparkclaw-preview:init", protocol: 1, document: {
          id: documentId, generation, loginGeneration, name: file.name, size: converted?.size ?? file.size,
          format: converted?.kind || (format === 'office' || format === 'xlsx' || format === 'csv' || format === 'tsv' ? 'unsupported' : format), language: zh ? "zh" : "en", position: initialPosition.current,
        } satisfies PreviewDocument }, PREVIEW_ORIGIN, [channel.port2]);
        initializing = false;
      } catch (reason) { if (active) { setError(localFileError(reason, zh)); if (invalidSource.has((reason as { code?: string })?.code || "")) callbacks.current.onInvalid(); revoke(); } }
    };
    // A reload/crash replaces the channel endpoint. Retired ports must never
    // serve the new document at the same WindowProxy.
    const navigated = () => { if (loaded) { revoke(); setError(zh ? "预览已重新载入，请重新打开。" : "The preview reloaded. Reopen it to continue."); } loaded = true; };
    const crashed = () => { revoke(); setError(zh ? "预览无法载入。" : "The preview could not load."); };
    window.addEventListener("message", ready);
    target.addEventListener("load", navigated); target.addEventListener("error", crashed);
    return () => { window.removeEventListener("message", ready); target.removeEventListener("load", navigated); target.removeEventListener("error", crashed); revoke(); };
  }, [files, file.ref, file.version, file.name, file.size, loginGeneration, zh, instance]);
  return <div className="localDocumentPreview">
    {error ? <div role="alert" className="localFilesError"><p>{error}</p><button type="button" onClick={() => { setError(""); setInstance(value => value + 1); }}>{zh ? "重新打开" : "Reopen"}</button></div> : <iframe key={instance} ref={frame} title={file.name}
      src={`${PREVIEW_ORIGIN}/index.html`} sandbox="allow-scripts allow-same-origin" referrerPolicy="no-referrer"
      allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-write 'none'" />}
  </div>;
}
