
import json, sys
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX reader requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
max_bytes = int(req.get("max_bytes") or 20000)

def trim(text):
    return " ".join(str(text or "").split())

try:
    prs = Presentation(req["path"])
    slides = []
    lines = []
    for s_index, slide in enumerate(prs.slides, start=1):
        items = []
        for shape_index, shape in enumerate(slide.shapes, start=1):
            if hasattr(shape, "text") and trim(shape.text):
                items.append({
                    "shape_index": shape_index,
                    "type": "text",
                    "text": trim(shape.text)
                })
            if hasattr(shape, "table"):
                rows = []
                for r_index, row in enumerate(shape.table.rows, start=1):
                    rows.append({"index": r_index, "cells": [trim(cell.text) for cell in row.cells]})
                items.append({"shape_index": shape_index, "type": "table", "rows": rows})
        slides.append({"index": s_index, "items": items})
        if items:
            lines.append("Slide %d:" % s_index)
            for item in items:
                if item["type"] == "text":
                    lines.append(item["text"])
                elif item["type"] == "table":
                    for row in item["rows"]:
                        lines.append("\t".join(row["cells"]))
    content = "\n".join(lines).strip()
    raw = content.encode("utf-8")
    truncated = len(raw) > max_bytes
    if truncated:
        content = raw[:max_bytes].decode("utf-8", errors="ignore")
    print(json.dumps({
        "content": content,
        "truncated": truncated,
        "document": {
            "schema_version": "document_read_v1",
            "format": "pptx",
            "slides": slides,
            "stats": {"slides": len(slides)}
        }
    }, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}))
