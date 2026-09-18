// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailMessage } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailRenderPreview } from "./emailRenderPreview";

let container: HTMLElement;
let root: ReturnType<typeof createRoot>;
const mail = { id: "mail", body_available: true } as EmailMessage;

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

it("loads once on demand and renders only the closed semantic tree", async () => {
  const read = vi.spyOn(api, "emailRenderPreview").mockResolvedValue({
    version: 1,
    id: "mail",
    state: "ready",
    sanitizer_version: "structured-mail-v3",
    representation_id: "repr",
    content: [
      { kind: "heading", level: 1, children: [{ kind: "text", text: "Account notice" }] },
      { kind: "paragraph", children: [{ kind: "text", text: "Code " }, { kind: "strong", children: [{ kind: "text", text: "632980" }] }] },
      { kind: "paragraph", children: [
        { kind: "link", url: "https://accounts.example.test/verify?token=abc", children: [{ kind: "text", text: "Verify account" }] },
        { kind: "text", text: " or " },
        { kind: "link", url: "javascript:alert(1)", children: [{ kind: "text", text: "unsafe action" }] }
      ] },
      { kind: "table", children: [{ kind: "table_body", children: [{ kind: "row", children: [{ kind: "cell", children: [{ kind: "text", text: "Layout row" }] }] }] }] },
      { kind: "table", children: [{ kind: "table_body", children: [
        { kind: "row", children: [{ kind: "cell", children: [{ kind: "text", text: "Item" }] }, { kind: "cell", children: [{ kind: "text", text: "Price" }] }] },
        { kind: "row", children: [{ kind: "cell", children: [{ kind: "text", text: "Plan" }] }, { kind: "cell", children: [{ kind: "text", text: "$20" }] }] }
      ] }] },
      { kind: "image", source: "https://tracker.example/pixel", alt: "invalid remote image" },
      { kind: "image", source: "data:image/png;base64,iVBORw0KGgo=", alt: "Brand" },
      { kind: "paragraph", children: [{ kind: "text", text: "<script>unsafe()</script>" }] }
    ],
    embedded_resource_count: 1,
    embedded_resource_bytes: 12
  });
  await act(async () => root.render(<EmailRenderPreview mail={mail} text={dictionaries.en} />));
  expect(read).not.toHaveBeenCalled();
  const toggle = async (open: boolean) => act(async () => {
    const details = container.querySelector("details")!;
    details.open = open;
    details.dispatchEvent(new Event("toggle"));
  });
  await toggle(true);
  expect(read).toHaveBeenCalledTimes(1);
  expect(container.querySelector("iframe")).toBeNull();
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("h2")?.textContent).toBe("Account notice");
  expect(container.querySelector("strong")?.textContent).toBe("632980");
  const externalLink = container.querySelector<HTMLAnchorElement>(".emailContentLink");
  expect(externalLink?.href).toBe("https://accounts.example.test/verify?token=abc");
  expect(externalLink?.target).toBe("_blank");
  expect(externalLink?.rel).toBe("noopener noreferrer nofollow");
  expect(externalLink?.getAttribute("referrerpolicy")).toBe("no-referrer");
  expect(externalLink?.textContent).toContain("Verify account");
  expect(externalLink?.textContent).toContain("accounts.example.test");
  expect(container.querySelectorAll("a")).toHaveLength(1);
  expect(container.textContent).toContain("unsafe action");
  expect(container.querySelector(".emailContentLayout")?.textContent).toBe("Layout row");
  expect(container.querySelectorAll("table")).toHaveLength(1);
  expect(container.querySelector("td")?.textContent).toBe("Item");
  expect(container.textContent).toContain("<script>unsafe()</script>");
  expect(container.querySelectorAll("img")).toHaveLength(1);
  expect(container.querySelector("img")?.getAttribute("src")).toMatch(/^data:image\/png;base64,/);
  await toggle(false);
  await toggle(true);
  expect(read).toHaveBeenCalledTimes(1);
});

it("shows an explicit unavailable state without falling back to plain text", async () => {
  vi.spyOn(api, "emailRenderPreview").mockResolvedValue({
    version: 1,
    id: "mail",
    state: "unavailable",
    sanitizer_version: "structured-mail-v3",
    representation_id: "repr",
    content: [],
    embedded_resource_count: 0,
    embedded_resource_bytes: 0,
    failure_code: "preview_not_migrated"
  });
  await act(async () => root.render(<EmailRenderPreview mail={mail} text={dictionaries.en} />));
  await act(async () => {
    const details = container.querySelector("details")!;
    details.open = true;
    details.dispatchEvent(new Event("toggle"));
  });
  expect(container.textContent).toContain(dictionaries.en.email.previewUnavailable);
  expect(container.querySelector("iframe")).toBeNull();
});
