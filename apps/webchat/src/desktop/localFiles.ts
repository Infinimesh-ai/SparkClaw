// This bridge is provided only to the signed-in desktop workbench top frame.
// It has no relationship to the conversation attachment capability.
export type LocalFile = {
  ref: string; locator: string; name: string; kind: "directory" | "file" | "link" | "bundle" | "unsupported";
  size: number; version: string; displayPath: string; unavailable?: string;
};
export type ReadingPosition = { page?: number; zoom?: number; scrollTop?: number; scrollLeft?: number; offset?: number; sheet?: string };
export type LocalReadingState = {
  tabs: { locator: string; position?: ReadingPosition }[]; active: string;
  expanded: string[]; selected?: string; scrollTop: number; showHidden: boolean; filter: string;
};
export type LocalListing = {
  entries: LocalFile[]; cursor?: string | null; complete: boolean; page: number; listing: string;
  totalLoaded?: number; pageSize?: number; changed?: boolean; incomplete?: string;
};
export type LocalFileChange = { kind: "changed" | "revoked"; refs?: string[]; generation: string };
export type LocalPreview = { previewId: string; kind: "pdf" | "spreadsheet"; size: number; warnings?: string[]; model?: unknown };
export interface LocalFilesBridge {
  envelopeVersion?: number;
  roots(): Promise<{ roots: LocalFile[]; generation: string }>;
  resolve(locator: string): Promise<LocalFile>;
  stat(ref: string): Promise<LocalFile>;
  list(options: { ref: string; cursor?: string; showHidden?: boolean; filter?: string; refresh?: boolean; page?: number }): Promise<LocalListing>;
  readBytes(options: { ref: string; version: string; offset: number; length: number; request_id?: string }): Promise<{ bytes: Uint8Array; offset: number; total: number; version: string }>;
  readText(options: { ref: string; version: string; offset: number; length: number; request_id?: string }): Promise<{ text: string }>;
  relativeImage(options: { ref: string; version: string; path: string }): Promise<{ bytes: Uint8Array; mime: string; version: string }>;
  followLink(options: { ref: string; ancestors: string[] }): Promise<LocalFile>;
  watch(ref: string): Promise<unknown>; unwatch(ref: string): Promise<unknown>; release(ref: string): Promise<unknown>;
  setVisible(visible: boolean): Promise<unknown>; cancel(request_id: string): Promise<unknown>;
  loadState(): Promise<{ state: LocalReadingState | null; generation: string }>;
  saveState(state: LocalReadingState): Promise<unknown>; clear(): Promise<unknown>;
  preview(options: { ref: string; version: string; kind: "office" | "xlsx" | "csv" | "tsv"; readerId: string }): Promise<LocalPreview>;
  readPreview(options: { previewId: string; readerId: string; offset: number; length: number }): Promise<{ bytes: Uint8Array }>;
  releasePreview(readerId: string): Promise<unknown>; cancelPreview(readerId: string): Promise<unknown>;
  onChange(listener: (event: LocalFileChange) => void): () => void;
}
declare global { interface Window { sparkclawLocalFiles?: LocalFilesBridge } }
const adapters = new WeakMap<LocalFilesBridge, LocalFilesBridge>();
export function localFilesCapability() {
  const bridge = typeof window === "undefined" ? undefined : window.sparkclawLocalFiles;
  if (!bridge || bridge.envelopeVersion !== 1) return bridge;
  let adapter = adapters.get(bridge);
  if (!adapter) {
    adapter = { onChange: bridge.onChange } as LocalFilesBridge;
    for (const method of ["roots", "resolve", "stat", "list", "readBytes", "readText", "relativeImage", "followLink", "watch", "unwatch", "release", "setVisible", "cancel", "loadState", "saveState", "clear", "preview", "readPreview", "releasePreview", "cancelPreview"] as const) {
      Object.assign(adapter, { [method]: async (...args: unknown[]) => {
        const result = await (bridge[method] as (...values: unknown[]) => Promise<unknown>)(...args) as { ok: boolean; value?: unknown; error?: { code: string; message: string } };
        if (!result?.ok) throw Object.assign(new Error(result?.error?.message || "Local files are unavailable"), { code: result?.error?.code || "unavailable" });
        return result.value;
      } });
    }
    adapters.set(bridge, adapter);
  }
  return adapter;
}
export function localFileError(error: unknown, zh: boolean) {
  const code = (error as { code?: string })?.code;
  const messages: Record<string, [string, string]> = {
    source_changed: ["文件已更改，请重新打开。", "This file changed. Reopen it to read its new version."],
    permission_denied: ["没有读取此位置的权限。", "Permission to read this location was denied."],
    locked: ["请先登录 SparkX。", "Sign in to SparkX first."],
    stale_cursor: ["目录已更改，请刷新。", "The directory changed. Refresh its listing."],
    unavailable: ["此文件目前无法在本机预览。", "This file is unavailable for local preview."],
    limit_exceeded: ["已达到本地预览限制，请缩小范围后重试。", "The local preview limit was reached. Narrow the selection and retry."],
    entry_limit: ["目录列表达到上限，请按名称缩小范围后刷新。", "The listing limit was reached. Narrow the name filter and refresh."],
    storage_limit: ["本地列表缓存已满，请缩小范围或清除阅读缓存。", "The local listing cache is full. Narrow the filter or clear reading caches."],
    resource_limit: ["请先收起一些目录或关闭文件标签页。", "Collapse some folders or close document tabs first."],
    cloud_placeholder: ["请先通过操作系统下载此文件。", "Download this file through the operating system first."],
    remote_location: ["此网络位置仅显示名称，请通过操作系统打开。", "This network location shows metadata only. Open it through the operating system."],
    link_cycle: ["此链接指向上级目录，无法循环展开。", "This link points to an ancestor and cannot be expanded here."],
    reference_expired: ["此位置的读取引用已失效，请重新打开。", "This reading reference expired. Reopen the location."],
    cleanup_pending: ["本地预览清理尚未完成，请清除缓存后重试。", "Local preview cleanup is pending. Clear the cache and retry."],
    preview_unavailable: ["此安装包暂不支持该文件的本地预览。", "This package cannot preview this file locally yet."],
  };
  return code && messages[code] ? messages[code][zh ? 0 : 1] : error instanceof Error ? error.message : zh ? "读取失败，请重试。" : "Reading failed. Try again.";
}
