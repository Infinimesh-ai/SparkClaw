export type WorkbenchDraft = { content: string; attachment_ids: string[]; revision: number };
export type DraftStatus = "loading" | "saved" | "saving" | "unsaved" | "error";
export type WorkbenchDraftAdapter = {
  load: (id: string) => Promise<WorkbenchDraft>;
  save: (id: string, draft: WorkbenchDraft) => Promise<WorkbenchDraft>;
};
type Entry = { value: WorkbenchDraft; dirty: boolean; sequence: number; epoch: number;
  write?: Promise<WorkbenchDraft>; timer?: ReturnType<typeof setTimeout> };
const copy = (draft: WorkbenchDraft): WorkbenchDraft => ({ ...draft, attachment_ids: [...draft.attachment_ids] });

// Transport-independent debounce/CAS queue. A late save cannot overwrite newer
// edits; consumed revisions fence delayed writes at the repository boundary.
export class WorkbenchDrafts {
  private entries = new Map<string, Entry>();
  private loads = new Map<string, Promise<WorkbenchDraft>>();
  constructor(private adapter: WorkbenchDraftAdapter, private options: {
    debounceMS?: number;
    onChange?: (id: string, value: WorkbenchDraft, status: DraftStatus) => void;
    onError?: (error: unknown) => void;
  } = {}) {}

  async load(id: string): Promise<WorkbenchDraft> {
    const cached = this.entries.get(id);
    if (cached) return copy(cached.value);
    let pending = this.loads.get(id);
    if (!pending) {
      pending = this.adapter.load(id).then((value) => {
        this.entries.set(id, { value: copy(value), dirty: false, sequence: 0, epoch: 0 });
        this.options.onChange?.(id, copy(value), "saved");
        return copy(value);
      }).finally(() => this.loads.delete(id));
      this.loads.set(id, pending);
    }
    return pending;
  }

  edit(id: string, value: Pick<WorkbenchDraft, "content" | "attachment_ids">) {
    const entry = this.entry(id);
    entry.value = { ...copy({ ...value, revision: entry.value.revision }) };
    entry.dirty = true; entry.sequence++;
    clearTimeout(entry.timer);
    entry.timer = setTimeout(() => void this.flush(id).catch((error) => this.options.onError?.(error)), this.options.debounceMS ?? 250);
    this.options.onChange?.(id, copy(entry.value), "unsaved");
  }

  async flush(id: string): Promise<WorkbenchDraft> {
    const entry = this.entries.get(id);
    if (!entry) return this.load(id);
    clearTimeout(entry.timer); entry.timer = undefined;
    if (entry.write) {
      await entry.write;
      return this.flush(id);
    }
    if (!entry.dirty) return copy(entry.value);
    const sequence = entry.sequence; const epoch = entry.epoch;
    const value = copy(entry.value);
    this.options.onChange?.(id, value, "saving");
    const write = this.adapter.save(id, value).then((saved) => {
      if (entry.epoch !== epoch) return copy(entry.value);
      if (entry.sequence === sequence) { entry.value = copy(saved); entry.dirty = false; }
      else entry.value = { ...entry.value, revision: saved.revision };
      this.options.onChange?.(id, copy(entry.value), entry.dirty ? "unsaved" : "saved");
      return copy(entry.value);
    }).catch((error) => {
      if (entry.epoch === epoch) this.options.onChange?.(id, copy(entry.value), "error");
      throw error;
    }).finally(() => { if (entry.write === write) entry.write = undefined; });
    entry.write = write;
    await write;
    return entry.dirty ? this.flush(id) : copy(entry.value);
  }

  accept(id: string, value: WorkbenchDraft) {
    const entry = this.entry(id);
    clearTimeout(entry.timer); entry.timer = undefined;
    entry.epoch++; entry.sequence++; entry.value = copy(value); entry.dirty = false;
    this.options.onChange?.(id, copy(entry.value), "saved");
  }

  dispose() {
    for (const [id, entry] of this.entries) {
      clearTimeout(entry.timer);
      if (entry.dirty) void this.flush(id).catch((error) => this.options.onError?.(error));
    }
  }

  private entry(id: string) {
    const entry = this.entries.get(id);
    if (!entry) throw new Error("Draft is still loading");
    return entry;
  }
}
