#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { getDocument, OPS } from "pdfjs-dist/legacy/build/pdf.mjs";

function round(value) {
  return Math.round(Number(value) * 1000) / 1000;
}

function normalizeText(value) {
  return String(value ?? "")
    .normalize("NFKC")
    .replace(/[\u000b\r\n\t ]+/gu, " ")
    .trim();
}

function stableValue(value) {
  if (value === null || value === undefined) return null;
  if (ArrayBuffer.isView(value)) return Array.from(value, round);
  if (Array.isArray(value)) return value.map(stableValue);
  if (typeof value === "number") return round(value);
  if (typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value)
        .filter(([key]) => !["name", "loadedName"].includes(key))
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, item]) => [key, stableValue(item)]),
    );
  }
  return String(value);
}

function digest(value) {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}

const operatorNames = new Map(Object.entries(OPS).map(([name, code]) => [code, name]));

async function inspectPage(page) {
  const viewport = page.getViewport({ scale: 1 });
  const content = await page.getTextContent({ includeMarkedContent: false, disableNormalization: false });
  const items = content.items
    .filter((item) => typeof item.str === "string")
    .map((item, index) => {
      const [a, b, c, d, e, f] = item.transform;
      const height = Math.max(Math.hypot(c, d), Math.abs(item.height || 0));
      const width = Math.abs(item.width || Math.hypot(a, b));
      const style = content.styles[item.fontName] ?? {};
      return {
        index,
        text: normalizeText(item.str),
        raw_text: item.str,
        x: round(e),
        y: round(viewport.height - f - height),
        width: round(width),
        height: round(height),
        transform: [a, b, c, d, e, f].map(round),
        font_ref: item.fontName,
        font_family: String(style.fontFamily ?? ""),
        vertical: Boolean(style.vertical),
        has_eol: Boolean(item.hasEOL),
      };
    });
  const operators = await page.getOperatorList();
  const normalizedOperators = operators.fnArray.map((code, index) => ({
    op: operatorNames.get(code) ?? `op_${code}`,
    args: stableValue(operators.argsArray[index]),
  }));
  const textGeometry = items.map(({ raw_text, font_ref, ...item }) => item);
  return {
    page: page.pageNumber,
    width: round(viewport.width),
    height: round(viewport.height),
    text: normalizeText(items.map((item) => item.text).join(" ")),
    items,
    operator_digest: digest(normalizedOperators),
    normalized_digest: digest({
      width: round(viewport.width),
      height: round(viewport.height),
      items: textGeometry,
      operator_digest: digest(normalizedOperators),
    }),
  };
}

async function main() {
  const [inputPath, outputPath] = process.argv.slice(2);
  if (!inputPath || !outputPath) {
    throw new Error("usage: pdfjs_probe.mjs INPUT.pdf OUTPUT.json");
  }
  const data = new Uint8Array(await readFile(inputPath));
  const document = await getDocument({ data, disableWorker: true, useSystemFonts: false }).promise;
  const pages = [];
  for (let pageNumber = 1; pageNumber <= document.numPages; pageNumber += 1) {
    pages.push(await inspectPage(await document.getPage(pageNumber)));
  }
  const result = {
    schema_version: "sparkclaw.pptx_overlength.pdfjs_probe.v1",
    pdfjs_version: "5.4.394",
    pages,
    normalized_digest: digest(pages.map(({ items, ...page }) => ({
      ...page,
      items: items.map(({ raw_text, font_ref, ...item }) => item),
    }))),
  };
  await writeFile(outputPath, `${JSON.stringify(result, null, 2)}\n`, "utf8");
}

main().catch((error) => {
  process.stderr.write(`${error.stack ?? error}\n`);
  process.exitCode = 1;
});
