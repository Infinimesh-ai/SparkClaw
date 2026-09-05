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
