// A failed save leaves the inserted text visible. Retrying that same recording
// must save the original draft, not insert its transcript a second time.
export class VoiceDraftPersistence {
  private applied?: { key: string; flush: () => Promise<unknown> };

  async apply(key: string, insert: () => boolean, flush: () => Promise<unknown>, wasApplied?: () => Promise<boolean>): Promise<boolean> {
    if (wasApplied && await wasApplied()) return true;
    if (this.applied?.key !== key) {
      if (!insert()) return false;
      this.applied = { key, flush };
    }
    await this.applied.flush();
    return true;
  }
}
