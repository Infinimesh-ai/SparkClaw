import { describe, expect, it } from "vitest";
import { parseMessageDisclosure } from "./messageDisclosure";

describe("message disclosure", () => {
  const content = "Final result.\n\n<details>\n<summary>Any translated label</summary>\n\nFact [1]\n[1] https://example.com\n</details>";

  it("retains evidence and sources independently of the summary label", () => {
    expect(parseMessageDisclosure(content)).toEqual({
      answer: "Final result.", details: "Fact [1]\n[1] https://example.com"
    });
  });

  it("keeps every partial disclosure out of the streaming answer", () => {
    const start = content.indexOf("<details>");
    for (let end = start + 1; end < content.length; end++) {
      expect(parseMessageDisclosure(content.slice(0, end), true), `stream offset ${end}`).toEqual({ answer: "Final result." });
    }
    expect(parseMessageDisclosure(content, true)?.details).toContain("https://example.com");
  });

  it.each([
    "普通回答\n\n事实：\n这是普通段落。",
    "Result\n\n<details open>\n<summary>Title</summary>\n\nBody\n</details>",
    "Result\n\n<details>\n<summary>Title</summary>\n\nBody",
    "Result\n\n<details>\n<summary>Title</summary>\n\n<details>Nested</details>\n</details>",
    `${content}\nTrailing text`
  ])("leaves unsupported or incomplete final text literal", value => {
    expect(parseMessageDisclosure(value)).toBeNull();
  });
});
