const readline = require("node:readline");
const crypto = require("node:crypto");
const { chromium } = require("playwright");
const playwrightVersion = require("playwright/package.json").version;

const config = JSON.parse(process.env.SPARKCLAW_PLAYWRIGHT_DRIVER_CONFIG || "{}");
const timeout = Number(config.timeoutMS) > 0 ? Number(config.timeoutMS) : 30000;
const launchTimeout = Math.max(1000, timeout - Math.min(5000, Math.floor(timeout / 4)));
const launchOptions = {
  headless: Boolean(config.headless),
  viewport: config.headless ? { width: 1365, height: 768 } : null,
  acceptDownloads: true,
  timeout: launchTimeout,
};
if (config.executablePath) launchOptions.executablePath = config.executablePath;

let context;
let nextPageID = 1;
let nextSnapshotID = 1;
let selectedPageID = 0;
let closing = false;
const pageIDs = new WeakMap();
const pagesByID = new Map();
const snapshotStates = new Map();

function pageID(page) {
  let id = pageIDs.get(page);
  if (!id) {
    id = nextPageID++;
    pageIDs.set(page, id);
    pagesByID.set(id, page);
    page.on("close", () => {
      pagesByID.delete(id);
      snapshotStates.delete(id);
      if (selectedPageID === id) selectedPageID = 0;
    });
  }
  return id;
}

async function ensureContext() {
  if (context) return context;
  context = await chromium.launchPersistentContext(config.userDataDir, launchOptions);
  context.setDefaultTimeout(timeout);
  context.setDefaultNavigationTimeout(timeout);
  context.on("page", (page) => {
    selectedPageID = pageID(page);
  });
  for (const page of context.pages()) pageID(page);
  if (context.pages().length) selectedPageID = pageID(context.pages()[context.pages().length - 1]);
  return context;
}

async function selectedPage() {
  const browserContext = await ensureContext();
  const selected = pagesByID.get(selectedPageID);
  if (selected && !selected.isClosed()) return selected;
  const pages = browserContext.pages().filter((page) => !page.isClosed());
  if (pages.length) {
    const page = pages[pages.length - 1];
    selectedPageID = pageID(page);
    return page;
  }
  const page = await browserContext.newPage();
  selectedPageID = pageID(page);
  return page;
}

async function pageList() {
  const browserContext = await ensureContext();
  const pages = [];
  for (const page of browserContext.pages()) {
    if (page.isClosed()) continue;
    const id = pageID(page);
    pages.push({
      page_id: `page_${id}`,
      id,
      url: page.url(),
      title: await page.title().catch(() => ""),
      selected: id === selectedPageID,
    });
  }
  const text = pages.length
    ? pages.map((page) => `${page.selected ? "* " : ""}${page.page_id}: ${page.title || "Untitled"} (${page.url})`).join("\n")
    : "No open pages";
  return { pages, text };
}

async function settlePage(page) {
  await page.waitForLoadState("domcontentloaded", { timeout }).catch(() => {});
  await page.waitForTimeout(150);
}

async function navigate(page, url) {
  await page.goto(String(url || ""), { waitUntil: "domcontentloaded", timeout });
  await settlePage(page);
}

async function openPage(params) {
  const browserContext = await ensureContext();
  const openPages = browserContext.pages().filter((page) => !page.isClosed());
  let page;
  if (openPages.length === 1 && openPages[0].url() === "about:blank") {
    page = openPages[0];
  } else {
    page = await browserContext.newPage();
  }
  selectedPageID = pageID(page);
  await navigate(page, params.url);
  return pageList();
}

function numericPageID(value) {
  const normalized = String(value ?? "").trim().toLowerCase().replace(/^page_/, "");
  const id = Number.parseInt(normalized, 10);
  if (!Number.isInteger(id) || id <= 0) throw new Error(`invalid page id ${JSON.stringify(value)}`);
  return id;
}

function pageForID(value) {
  const id = numericPageID(value);
  const page = pagesByID.get(id);
  if (!page || page.isClosed()) throw new Error(`page_${id} is unavailable`);
  return { id, page };
}

async function focusPage(params) {
  const { id, page } = pageForID(params.pageId ?? params.page_id);
  await page.bringToFront();
  selectedPageID = id;
  return pageList();
}

async function closePage(params) {
  const { id, page } = pageForID(params.pageId ?? params.page_id);
  await page.close();
  pagesByID.delete(id);
  snapshotStates.delete(id);
  const remaining = (await ensureContext()).pages().filter((candidate) => !candidate.isClosed());
  if (remaining.length) selectedPageID = pageID(remaining[remaining.length - 1]);
  return pageList();
}

async function navigatePage(params) {
  let page;
  let id;
  if (params.pageId != null || params.page_id != null) {
    ({ id, page } = pageForID(params.pageId ?? params.page_id));
    selectedPageID = id;
  } else {
    page = await selectedPage();
    id = pageID(page);
  }
  const kind = String(params.type || "url").toLowerCase();
  if (kind === "back") await page.goBack({ waitUntil: "domcontentloaded", timeout });
  else if (kind === "forward") await page.goForward({ waitUntil: "domcontentloaded", timeout });
  else if (kind === "reload") await page.reload({ waitUntil: "domcontentloaded", timeout });
  else await navigate(page, params.url);
  await settlePage(page);
  return { page_id: `page_${id}`, url: page.url(), title: await page.title().catch(() => ""), readyState: await page.evaluate(() => document.readyState).catch(() => "") };
}

function stableHash(value) {
  return crypto.createHash("sha256").update(String(value || "")).digest("hex");
}

async function snapshotPage(params = {}) {
  let page;
  let id;
  if (params.pageId != null || params.page_id != null) {
    ({ id, page } = pageForID(params.pageId ?? params.page_id));
    selectedPageID = id;
  } else {
    page = await selectedPage();
    id = pageID(page);
  }
  const previousState = snapshotStates.get(id);
  const refs = await page.locator("a[href],button,input,textarea,select,[role='button'],[role='link'],[role='menuitem'],[role='tab'],[contenteditable='true'],[tabindex]").evaluateAll((elements) => {
    document.querySelectorAll("[data-sparkclaw-ref]").forEach((element) => element.removeAttribute("data-sparkclaw-ref"));
    const out = [];
    for (const element of elements) {
      if (out.length >= 1000) break;
      const style = window.getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      if (element.disabled || element.getAttribute("aria-hidden") === "true" || style.display === "none" || style.visibility === "hidden" || Number(style.opacity || "1") <= 0 || rect.width <= 0 || rect.height <= 0) continue;
      const shortRef = `e${out.length + 1}`;
      element.setAttribute("data-sparkclaw-ref", shortRef);
      const tag = element.tagName.toLowerCase();
      const implicitRole = tag === "a" ? "link" : tag === "button" ? "button" : tag === "select" ? "combobox" : tag === "textarea" ? "textbox" : tag === "input" ? (element.type === "checkbox" ? "checkbox" : element.type === "radio" ? "radio" : "textbox") : "control";
      const labels = element.labels ? Array.from(element.labels).map((label) => label.innerText || label.textContent || "") : [];
      const name = [element.getAttribute("aria-label"), ...labels, element.innerText, element.getAttribute("placeholder"), element.getAttribute("title"), element.getAttribute("name")]
        .find((value) => value && value.trim()) || "";
      const containerElement = element.closest('[role="dialog"],[role="form"],form,nav,main,section,article');
      const container = containerElement
        ? [containerElement.getAttribute("aria-label"), containerElement.getAttribute("title"), containerElement.querySelector("h1,h2,h3,legend")?.textContent]
          .find((value) => value && value.trim()) || containerElement.tagName.toLowerCase()
        : "";
      const nearbyText = (element.parentElement?.innerText || element.parentElement?.textContent || "").replace(/\s+/g, " ").trim().slice(0, 320);
      out.push({
        short_ref: shortRef,
        role: element.getAttribute("role") || implicitRole,
        accessible_name: name.replace(/\s+/g, " ").trim().slice(0, 240),
        tag,
        type: String(element.type || ""),
        target_url: tag === "a" ? String(element.href || "").slice(0, 1000) : "",
        visible: true,
        enabled: !element.disabled,
        expanded: element.getAttribute("aria-expanded") === "true",
        container: String(container).replace(/\s+/g, " ").trim().slice(0, 240),
        nearby_text: nearbyText,
        in_viewport: rect.bottom > 0 && rect.right > 0 && rect.top < window.innerHeight && rect.left < window.innerWidth,
        checked: typeof element.checked === "boolean" ? element.checked : undefined,
        selected: typeof element.selected === "boolean" ? element.selected : undefined,
      });
    }
    return out;
  });
  let aria = "";
  try {
    aria = await page.locator("body").ariaSnapshot({ timeout });
  } catch {
    aria = await page.locator("body").innerText({ timeout }).catch(() => "");
  }
  const title = await page.title().catch(() => "");
  const interactionGoal = String(params.interaction_goal || "").trim();
  const rankedRefs = refs.map((item, index) => {
    const name = String(item.accessible_name || "").toLowerCase();
    const context = `${item.container || ""} ${item.nearby_text || ""}`.toLowerCase();
    const goal = interactionGoal.toLowerCase();
    let score = item.in_viewport ? 20 : 0;
    if (item.role === "button") score += 5;
    if (name && goal.includes(name)) score += 200;
    if (name && name.includes(goal)) score += 100;
    for (const token of goal.split(/\s+/).filter((value) => value.length > 1)) {
      if (name.includes(token)) score += 30;
      else if (context.includes(token)) score += 5;
    }
    return { item, index, score };
  }).sort((left, right) => right.score - left.score || left.index - right.index);
  const returnedRefs = rankedRefs.slice(0, 24).map(({ item }) => item);
  const digestInput = JSON.stringify({ url: page.url(), title, aria, controls: refs.map(({ short_ref, ...item }) => item) });
  const digest = stableHash(digestInput);
  const snapshotID = `snapshot_${id}_${nextSnapshotID++}`;
  const duplicateCounts = new Map();
  const controls = returnedRefs.map((item) => {
    const ordinalKey = `${item.role}\u0000${item.accessible_name}`;
    const ordinal = (duplicateCounts.get(ordinalKey) || 0) + 1;
    duplicateCounts.set(ordinalKey, ordinal);
    const fingerprint = stableHash(JSON.stringify({
      role: item.role, name: item.accessible_name, tag: item.tag, type: item.type,
      target_url: item.target_url, container: item.container, ordinal,
    }));
    const ref = `${snapshotID}:${item.short_ref}:${fingerprint.slice(0, 16)}`;
    return { ...item, ref, ordinal, fingerprint };
  });
  const repeated = Boolean(previousState?.actionTaken && previousState.digest === digest);
  snapshotStates.set(id, {
    snapshotID,
    digest,
    actionTaken: false,
    refs: new Map(controls.map((item) => [item.ref, item])),
  });
  const refText = controls.map((item) => {
    const fields = [`ref=${item.ref}`, `role=${item.role}`];
    if (item.accessible_name) fields.push(`name=${JSON.stringify(item.accessible_name)}`);
    if (item.type) fields.push(`type=${item.type}`);
    if (item.target_url) fields.push(`href=${item.target_url}`);
    return `- ${fields.join(" ")}`;
  }).join("\n");
  const text = [`Page: ${page.url()}`, aria, controls.length ? `Interactive refs:\n${refText}` : "Interactive refs: none"].filter(Boolean).join("\n");
  const snapshot = {
    schema_version: "browser_interaction_snapshot_v1",
    snapshot_id: snapshotID,
    previous_snapshot_id: previousState?.actionTaken ? previousState.snapshotID : "",
    page_id: `page_${id}`,
    url: page.url(),
    title,
    interaction_goal: interactionGoal,
    digest,
    repeated,
    controls_total: refs.length,
    controls_returned: controls.length,
    truncated: refs.length > controls.length,
    aria,
    controls,
    refs: controls,
  };
  return { text, snapshot_id: snapshotID, page_id: `page_${id}`, digest, repeated, snapshot, content: [{ type: "text", text }] };
}

async function locatorForRef(params) {
  let page;
  let id;
  if (params.pageId != null || params.page_id != null) {
    ({ id, page } = pageForID(params.pageId ?? params.page_id));
    selectedPageID = id;
  } else {
    page = await selectedPage();
    id = pageID(page);
  }
  const uid = String(params.uid ?? params.ref ?? "").trim();
  const state = snapshotStates.get(id);
  const snapshotID = String(params.snapshot_id ?? params.snapshotId ?? "").trim();
  if (!state || state.actionTaken || (snapshotID && state.snapshotID !== snapshotID)) throw new Error(`stale or unknown snapshot ${JSON.stringify(snapshotID)}; take a new browser.snapshot`);
  const descriptor = state.refs.get(uid);
  if (!uid || !descriptor) throw new Error(`stale or unknown snapshot ref ${JSON.stringify(uid)}; take a new browser.snapshot`);
  const locator = page.locator(`[data-sparkclaw-ref="${descriptor.short_ref}"]`);
  if (await locator.count() !== 1) throw new Error(`snapshot ref ${uid} is detached or ambiguous; take a new browser.snapshot`);
  const current = await locator.evaluate((element) => {
    const tag = element.tagName.toLowerCase();
    const implicitRole = tag === "a" ? "link" : tag === "button" ? "button" : tag === "select" ? "combobox" : tag === "textarea" ? "textbox" : tag === "input" ? (element.type === "checkbox" ? "checkbox" : element.type === "radio" ? "radio" : "textbox") : "control";
    const labels = element.labels ? Array.from(element.labels).map((label) => label.innerText || label.textContent || "") : [];
    const name = [element.getAttribute("aria-label"), ...labels, element.innerText, element.getAttribute("placeholder"), element.getAttribute("title"), element.getAttribute("name")]
      .find((value) => value && value.trim()) || "";
    const containerElement = element.closest('[role="dialog"],[role="form"],form,nav,main,section,article');
    const container = containerElement
      ? [containerElement.getAttribute("aria-label"), containerElement.getAttribute("title"), containerElement.querySelector("h1,h2,h3,legend")?.textContent]
        .find((value) => value && value.trim()) || containerElement.tagName.toLowerCase()
      : "";
    return {
      role: element.getAttribute("role") || implicitRole,
      name: name.replace(/\s+/g, " ").trim().slice(0, 240),
      tag,
      type: String(element.type || ""),
      target_url: tag === "a" ? String(element.href || "").slice(0, 1000) : "",
      container: String(container).replace(/\s+/g, " ").trim().slice(0, 240),
      visible: Boolean(element.getClientRects().length),
      enabled: !element.disabled,
    };
  });
  const currentFingerprint = stableHash(JSON.stringify({
    role: current.role, name: current.name, tag: current.tag, type: current.type,
    target_url: current.target_url, container: current.container, ordinal: descriptor.ordinal,
  }));
  if (currentFingerprint !== descriptor.fingerprint || !current.visible || !current.enabled) throw new Error(`snapshot ref ${uid} changed or is unavailable; take a new browser.snapshot`);
  return { page, id, locator, descriptor, state };
}

async function clickRef(params) {
  const { page, id, locator, descriptor, state } = await locatorForRef(params);
  const beforeURL = page.url();
  await locator.click({ timeout });
  await settlePage(page);
  state.actionTaken = true;
  return {
    clicked: String(params.uid ?? params.ref),
    snapshot_id: state.snapshotID,
    page_id: `page_${id}`,
    fingerprint: descriptor.fingerprint,
    role: descriptor.role,
    accessible_name: descriptor.accessible_name,
    before_url: beforeURL,
    url: page.url(),
    url_changed: beforeURL !== page.url(),
  };
}

async function fillRef(params) {
  const { locator } = await locatorForRef(params);
  await locator.fill(String(params.value ?? ""), { timeout });
  return { filled: String(params.uid ?? params.ref) };
}

async function typeText(params) {
  const page = await selectedPage();
  await page.keyboard.type(String(params.text ?? ""), { delay: Number(params.delay) || 0 });
  return { typed: true, url: page.url() };
}

async function selectRef(params) {
  const { locator } = await locatorForRef(params);
  const selected = await locator.selectOption(String(params.value ?? ""), { timeout });
  return { selected, ref: String(params.uid ?? params.ref) };
}

async function waitFor(params) {
  let page;
  if (params.pageId != null || params.page_id != null) {
    ({ page } = pageForID(params.pageId ?? params.page_id));
  } else {
    page = await selectedPage();
  }
  const text = String(params.text || "").trim();
  if (text) await page.getByText(text, { exact: false }).first().waitFor({ state: "visible", timeout });
  else await settlePage(page);
  return { text, visible: text ? true : undefined, settled: true, url: page.url() };
}

async function screenshotPage(params) {
  const page = await selectedPage();
  const data = await page.screenshot({ type: "png", fullPage: Boolean(params.fullPage ?? params.full_page) });
  return { content: [{ type: "image", data: data.toString("base64"), mimeType: "image/png" }], url: page.url() };
}

async function evaluateScript(params) {
  const page = await selectedPage();
  const source = String(params.function || "");
  if (!source || source.length > 250000) throw new Error("internal evaluation source is empty or too large");
  const result = await page.evaluate(async (fnSource) => {
    const fn = (0, eval)(`(${fnSource})`);
    return await fn();
  }, source);
  return { result };
}

async function pageState() {
  const page = await selectedPage();
  return { url: page.url(), title: await page.title().catch(() => ""), readyState: await page.evaluate(() => document.readyState).catch(() => "") };
}

async function closeDriver() {
  if (closing) return { closed: true };
  closing = true;
  if (context) await context.close().catch(() => {});
  context = undefined;
  return { closed: true };
}

async function dispatch(method, params) {
  switch (method) {
    case "health": {
      const browserContext = await ensureContext();
      return { ok: true, provider: "microsoft-playwright", playwright_version: playwrightVersion, browser_version: browserContext.browser()?.version() || "", page_count: browserContext.pages().length };
    }
    case "list_pages": return pageList();
    case "new_page": return openPage(params);
    case "select_page": return focusPage(params);
    case "close_page": return closePage(params);
    case "navigate_page": return navigatePage(params);
    case "take_snapshot": return snapshotPage(params);
    case "take_screenshot": return screenshotPage(params);
    case "wait_for": return waitFor(params);
    case "click": return clickRef(params);
    case "fill": return fillRef(params);
    case "type_text": return typeText(params);
    case "select_option": return selectRef(params);
    case "evaluate_script": return evaluateScript(params);
    case "page_state": return pageState();
    case "close": return closeDriver();
    default: throw new Error(`unsupported Playwright driver method ${JSON.stringify(method)}`);
  }
}

function send(response) {
  process.stdout.write(`${JSON.stringify(response)}\n`);
}

const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
let queue = Promise.resolve();
lines.on("line", (line) => {
  queue = queue.then(async () => {
    let request;
    try {
      request = JSON.parse(line);
      const result = await dispatch(request.method, request.params || {});
      send({ id: request.id, result });
      if (request.method === "close") process.exitCode = 0;
    } catch (error) {
      send({ id: request?.id, error: { code: "playwright_action_failed", message: String(error?.message || error) } });
    }
  });
});

lines.on("close", () => {
  queue.finally(async () => {
    await closeDriver();
    process.exit(0);
  });
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, async () => {
    await closeDriver();
    process.exit(0);
  });
}
