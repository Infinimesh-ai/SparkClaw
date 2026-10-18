// Publish in increasing read-start order. An in-flight newer read must not
// starve visible updates; a slower old read cannot replace a newer publication.
export class ConversationReadVersion {
  private version = 0;
  private committed = 0;
  async read<T>(fetch: () => Promise<T>, current: () => boolean, commit: (value: T) => void) {
    if (!current()) return;
    const version = ++this.version;
    const value = await fetch();
    if (version > this.committed && current()) { this.committed = version; commit(value); }
  }
}
