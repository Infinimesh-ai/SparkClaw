import json, sys, unicodedata
try:
    from pypdf import PdfReader
except Exception:
    print("PDF index extractor requires pypdf", file=sys.stderr)
    sys.exit(1)

# Text-only extraction of one page batch for the document index. It never
# renders or OCRs pages; pages without usable text are reported as unindexed
# so a later analysis phase can recognize only the pages it needs.

req = json.load(sys.stdin)
start = max(0, int(req.get("start") or 0))
count = max(1, int(req.get("count") or 1))

def meaningful_characters(text):
    return sum(1 for value in text if unicodedata.category(value)[:1] in ("L", "N"))

def outline_entries(reader):
    entries = []
    def walk(items, level):
        for item in items:
            if isinstance(item, list):
                walk(item, level + 1)
                continue
            try:
                page = reader.get_destination_page_number(item) + 1
            except Exception:
                continue
            title = " ".join(str(getattr(item, "title", "") or "").split())
            if title and page > 0:
                entries.append({"title": title, "level": level, "page": page})
    try:
        walk(reader.outline, 1)
    except Exception:
        return []
    return entries

try:
    reader = PdfReader(req["path"])
    total = len(reader.pages)
    pages = []
    for index in range(start, min(total, start + count)):
        try:
            text = reader.pages[index].extract_text() or ""
        except Exception:
            text = ""
        text = text.strip()
        pages.append({"page": index + 1, "text": text, "indexed": meaningful_characters(text) >= 3})
    out = {"pages_total": total, "pages": pages}
    if req.get("outline"):
        out["outline"] = outline_entries(reader)
    print(json.dumps(out))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
