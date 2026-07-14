export function insertVoiceTranscript(value: string, transcript: string, start: number, end: number) {
  const clean = transcript.trim();
  if (!clean) return { value, caret: start };
  const before = value.slice(0, start);
  const after = value.slice(end);
  const leftSpace = needsASCIISpace(before.at(-1), clean[0]) ? " " : "";
  const rightSpace = needsASCIISpace(clean.at(-1), after[0]) ? " " : "";
  const inserted = `${leftSpace}${clean}${rightSpace}`;
  return {
    value: `${before}${inserted}${after}`,
    caret: before.length + inserted.length
  };
}

function needsASCIISpace(left?: string, right?: string) {
  return Boolean(left && right && /[A-Za-z0-9]/.test(left) && /[A-Za-z0-9]/.test(right));
}
