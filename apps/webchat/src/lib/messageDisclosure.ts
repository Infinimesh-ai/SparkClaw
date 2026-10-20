export type MessageDisclosure = { answer: string; details?: string };

// This is structural markup, independent of the human-facing summary label.
// It is never injected as HTML: both bodies use the ordinary text renderer.
// The pattern mirrors the Gateway plain-text parser (delivery/plaintext.go),
// including its ASCII-only trailing whitespace.
export function parseMessageDisclosure(content: string, inProgress = false): MessageDisclosure | null {
  const match = content.match(/^(.+?)\n\n<details>\n<summary>[^<>\n]*<\/summary>\n\n(.+)\n<\/details>[ \t\n\f\r]*$/s);
  if (match && !match[1].includes("<details>") && !/<\/?details>/.test(match[2])) {
    return { answer: match[1].trim(), details: match[2].trim() };
  }
  if (inProgress) {
    // Smooth reveal may stop at any character, including inside the opening
    // tag. Keep the result readable while the disclosure is still arriving.
    const boundary = content.lastIndexOf("\n\n<");
    if (boundary > 0) {
      const tail = content.slice(boundary + 2);
      if ("<details>".startsWith(tail) || tail.startsWith("<details>\n")) {
        return { answer: content.slice(0, boundary).trim() };
      }
    }
  }
  return null;
}
