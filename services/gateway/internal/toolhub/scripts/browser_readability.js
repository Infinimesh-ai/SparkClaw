const fs = require("fs");

function write(value) {
  process.stdout.write(JSON.stringify(value));
}

function text(value) {
  return String(value || "").replace(/\s+/g, " ").trim();
}

let input = {};
try {
  const raw = fs.readFileSync(0, "utf8");
  input = raw.trim() ? JSON.parse(raw) : {};
} catch (error) {
  write({ ok: false, reason: "invalid_input: " + (error && error.message ? error.message : String(error)) });
  process.exit(0);
}

try {
  const { Readability, isProbablyReaderable } = require("@mozilla/readability");
  const { JSDOM } = require("jsdom");

  const html = String(input.html || "");
  if (!html.trim()) {
    write({ ok: false, reason: "empty_html" });
    process.exit(0);
  }

  const dom = new JSDOM(html, {
    url: String(input.url || "https://example.invalid/"),
    contentType: "text/html",
  });
  const document = dom.window.document;
  const readerable = isProbablyReaderable(document, {
    minContentLength: 80,
    minScore: 10,
  });
  const reader = new Readability(document.cloneNode(true), {
    charThreshold: 80,
  });
  const article = reader.parse();
  const articleText = text(article && article.textContent);
  if (!article || !articleText) {
    write({
      ok: false,
      readerable,
      reason: readerable ? "parse_returned_empty" : "not_readerable",
    });
    process.exit(0);
  }

  write({
    ok: true,
    readerable,
    title: text(article.title),
    text: articleText,
    length: article.length || articleText.length,
    excerpt: text(article.excerpt),
    byline: text(article.byline),
    dir: text(article.dir),
    siteName: text(article.siteName),
    lang: text(article.lang),
    publishedTime: text(article.publishedTime),
  });
} catch (error) {
  write({
    ok: false,
    reason: error && error.message ? error.message : String(error),
  });
}
