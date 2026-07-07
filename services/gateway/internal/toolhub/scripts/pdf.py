
import json, sys, os
try:
    from pypdf import PdfReader, PdfWriter
except Exception:
    print(json.dumps({"error":"PDF adapter requires pypdf"}))
    sys.exit(0)

req = json.load(sys.stdin)
op = req.get("operation")

def page_indexes(pages, count):
    if not pages:
        raise ValueError("pages are required")
    out = []
    for page in pages:
        idx = int(page) - 1
        if idx < 0 or idx >= count:
            raise ValueError("page out of range: %s" % page)
        out.append(idx)
    return out

try:
    if op in ("extract_text", "read"):
        reader = PdfReader(req["path"])
        pages = []
        chunks = []
        for index, page in enumerate(reader.pages, start=1):
            text = page.extract_text() or ""
            pages.append({"index": index, "text": text.strip()})
            chunks.append(text)
        text = "\n\n".join(chunks).strip()
        if not text:
            out = {"content":"","truncated":False,"scanned_unsupported":True}
            if op == "read":
                out["document"] = {"schema_version":"document_read_v1","format":"pdf","pages":pages,"stats":{"pages":len(pages)}}
            print(json.dumps(out))
            sys.exit(0)
        max_bytes = int(req.get("max_bytes") or 20000)
        raw = text.encode("utf-8")
        truncated = len(raw) > max_bytes
        if truncated:
            text = raw[:max_bytes].decode("utf-8", errors="ignore")
        out = {"content":text,"truncated":truncated,"scanned_unsupported":False}
        if op == "read":
            out["document"] = {"schema_version":"document_read_v1","format":"pdf","pages":pages,"stats":{"pages":len(pages)}}
        print(json.dumps(out))
    elif op == "merge":
        writer = PdfWriter()
        for path in req["inputs"]:
            reader = PdfReader(path)
            for page in reader.pages:
                writer.add_page(page)
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages),"inputs":req["inputs"]}))
    elif op in ("extract_pages", "delete_pages", "rotate_pages"):
        reader = PdfReader(req["path"])
        writer = PdfWriter()
        selected = set(page_indexes(req.get("pages"), len(reader.pages)))
        if op == "rotate_pages":
            rotation = int(req.get("rotation") or 90)
            if rotation % 90 != 0:
                raise ValueError("rotation must be a multiple of 90")
        for i, page in enumerate(reader.pages):
            if op == "extract_pages" and i not in selected:
                continue
            if op == "delete_pages" and i in selected:
                continue
            if op == "rotate_pages" and i in selected:
                page.rotate(rotation)
            writer.add_page(page)
        if len(writer.pages) == 0:
            raise ValueError("operation would produce an empty PDF")
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages)}))
    elif op == "split":
        reader = PdfReader(req["path"])
        if len(reader.pages) == 0:
            raise ValueError("cannot split an empty PDF")
        base, ext = os.path.splitext(req["output_path"])
        outputs = []
        for i, page in enumerate(reader.pages, start=1):
            writer = PdfWriter()
            writer.add_page(page)
            if len(reader.pages) == 1:
                part_path = req["output_path"]
            else:
                part_path = "%s-page-%d%s" % (base, i, ext or ".pdf")
            os.makedirs(os.path.dirname(part_path), exist_ok=True)
            with open(part_path, "wb") as f:
                writer.write(f)
            outputs.append(part_path)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"outputs":outputs,"bytes":sum(os.path.getsize(p) for p in outputs),"pages":len(reader.pages)}))
    else:
        print(json.dumps({"error":"unsupported pdf operation: %s" % op}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
