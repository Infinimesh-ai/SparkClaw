// These are conservative admission limits, not claims about a process hard cap.
export const LOCAL_PREVIEW_LIMITS = Object.freeze({
  inputBytes: 32 * 1024 * 1024, aggregateInputBytes: 64 * 1024 * 1024,
  outputBytes: 32 * 1024 * 1024, modelBytes: 8 * 1024 * 1024,
  archiveEntries: 4096, archiveEntryBytes: 32 * 1024 * 1024, archiveBytes: 128 * 1024 * 1024,
  sheets: 32, cells: 50_000, rectangle: 100_000, cellText: 32_768,
  readers: 12, queuedJobs: 8, deadlineMs: 90_000,
  cacheBytes: 96 * 1024 * 1024, cacheEntries: 12,
  taskDiskBytes: 256 * 1024 * 1024, diskBytes: 512 * 1024 * 1024,
  memoryBytes: 768 * 1024 * 1024, diskHeadroomBytes: 512 * 1024 * 1024,
  monitorMs: 250, killGraceMs: 500, cleanupMs: 5_000, readBytes: 1024 * 1024,
});

export function previewError(code, message) { return Object.assign(new Error(message), { code }); }
export function previewLimits(overrides = {}) {
  const result = { ...LOCAL_PREVIEW_LIMITS, ...overrides };
  if (Object.keys(overrides).some(key => !(key in LOCAL_PREVIEW_LIMITS)) ||
      Object.values(result).some(value => !Number.isSafeInteger(value) || value < 1)) {
    throw new TypeError('Invalid local preview limits');
  }
  return Object.freeze(result);
}
