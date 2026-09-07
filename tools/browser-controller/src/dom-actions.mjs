export const BACKGROUND_CLICK_FUNCTION = `(element) => {
  if (!(element instanceof Element) || !element.isConnected || typeof element.click !== "function") return false;
  const style = getComputedStyle(element);
  const rect = element.getBoundingClientRect();
  const disabled = element.matches(":disabled") || element.getAttribute("aria-disabled") === "true";
  if (disabled || rect.width <= 0 || rect.height <= 0 || style.display === "none" ||
      style.visibility === "hidden" || Number.parseFloat(style.opacity || "1") <= 0) return false;
  element.scrollIntoView({ block: "center", inline: "center" });
  element.click();
  return true;
}`;

export const BACKGROUND_FOCUS_FUNCTION = `(element) => {
  if (!element?.isConnected || typeof element.focus !== "function") return false;
  // Recipient editors can trap focus until an outside mousedown releases them.
  element.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, cancelable: true, button: 0, buttons: 1 }));
  element.focus();
  element.dispatchEvent(new MouseEvent("mouseup", { bubbles: true, cancelable: true, button: 0, buttons: 0 }));
  return document.activeElement === element;
}`;

export const EDITOR_LINES_FUNCTION = `(element) => {
  if (!element || element.getAttribute("contenteditable") !== "true") throw new Error("editor missing");
  const nodes = Array.from(element.childNodes);
  if (nodes.every(node => node.nodeType === Node.TEXT_NODE)) return element.textContent;
  if (!nodes.every(node => node.nodeType === Node.ELEMENT_NODE && node.tagName === "DIV" &&
      Array.from(node.childNodes).every(child => child.nodeType === Node.TEXT_NODE || child.tagName === "BR"))) {
    throw new Error("editor structure changed");
  }
  return nodes.map(node => node.childNodes.length === 1 && node.firstChild.tagName === "BR" ? "" :
    Array.from(node.childNodes).map(child => child.tagName === "BR" ? "\\n" : child.textContent).join("")).join("\\n");
}`;

export const BATCH_READ_FUNCTION = `async (commands, readLines) => {
  const visible = element => {
    if (!element || !element.isConnected) return false;
    const rect = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return rect.width > 0 && rect.height > 0 && style.display !== "none" &&
      style.visibility !== "hidden" && Number.parseFloat(style.opacity || "1") > 0;
  };
  // Capture all fields synchronously before hashing so they describe one DOM state.
  const values = commands.map(([name, subtype, selector, attribute]) => {
    if (subtype === "url") return location.href;
    if (subtype === "count") return document.querySelectorAll(selector).length;
    const element = document.querySelector(selector);
    if (subtype === "visible") return visible(element);
    if (subtype === "enabled") return Boolean(element) && !element.disabled && element.getAttribute("aria-disabled") !== "true";
    if (subtype === "value") return element?.value ?? (element?.isContentEditable ? element.textContent : "") ?? "";
    if (subtype === "attr") return element?.getAttribute(attribute) ?? "";
    if (subtype === "lines") return readLines(element);
    return element ? (element.innerText ?? element.textContent ?? "") : "";
  });
  return await Promise.all(values.map(async (value, index) => {
    if (typeof value !== "string" || commands[index][1] === "url") return { value };
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value))),
      byte => byte.toString(16).padStart(2, "0")).join("");
    return { value, digest };
  }));
}`;
