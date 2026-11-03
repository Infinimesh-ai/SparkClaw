import base64, io, json, sys, unicodedata
try:
    from pypdf import PdfReader
except Exception:
    print("PDF index extractor requires pypdf", file=sys.stderr)
    sys.exit(1)

# Text-only extraction of one page batch for the document index; pages
# without usable text are reported as unindexed. The "render" operation
# rasterizes only the pages an analysis-phase request selected for OCR, with
# the same settings as pdf.py's scanned-page rendering.

MAX_RENDER_PAGES = 8
MAX_RENDER_PAGE_BYTES = 4 << 20
MAX_RENDER_TOTAL_BYTES = 24 << 20

req = json.load(sys.stdin)
start = max(0, int(req.get("start") or 0))
count = max(1, int(req.get("count") or 1))

def render_pages(path, pages):
    try:
        import pypdfium2 as pdfium
        from PIL import Image
    except Exception:
        print("PDF page rendering requires pypdfium2 and Pillow", file=sys.stderr)
        sys.exit(1)
    document = pdfium.PdfDocument(path)
    out = []
    total = 0
    try:
        for number in pages[:MAX_RENDER_PAGES]:
            if not isinstance(number, int) or number < 1 or number > len(document):
                continue
            page = document[number - 1]
            bitmap = page.render(scale=2.0)
            try:
                image = bitmap.to_pil().convert("RGB")
                if max(image.size) > 2400:
                    image.thumbnail((2400, 2400), Image.Resampling.LANCZOS)
                data = None
                for attempt in range(6):
                    buffer = io.BytesIO()
                    image.save(buffer, format="JPEG", quality=max(62, 92 - attempt * 6), optimize=True)
                    if len(buffer.getvalue()) <= MAX_RENDER_PAGE_BYTES:
                        data = buffer.getvalue()
                        break
                    image = image.resize((max(1, image.width * 4 // 5), max(1, image.height * 4 // 5)), Image.Resampling.LANCZOS)
            finally:
                bitmap.close()
                page.close()
            if data is None or total + len(data) > MAX_RENDER_TOTAL_BYTES:
                continue
            total += len(data)
            out.append({"page": number, "content_type": "image/jpeg", "data_base64": base64.b64encode(data).decode("ascii")})
    finally:
        document.close()
    return out

if req.get("operation") == "render":
    try:
        print(json.dumps({"pages": render_pages(req["path"], list(req.get("pages") or []))}))
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
    sys.exit(0)

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
