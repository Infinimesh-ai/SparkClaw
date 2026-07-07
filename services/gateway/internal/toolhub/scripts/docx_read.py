
import json, sys
try:
    import docx
except Exception:
    print(json.dumps({"error":"DOCX reader requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
max_bytes = int(req.get("max_bytes") or 20000)

def trim(text):
    return " ".join(str(text or "").split())

try:
    document = docx.Document(req["path"])
    paragraphs = []
    blocks = []
    tables = []
    lines = []
    block_index = 0

    for index, paragraph in enumerate(document.paragraphs, start=1):
        text = trim(paragraph.text)
        if not text:
            continue
        block_index += 1
        location = {
            "part": "document",
            "block_type": "paragraph",
            "block_index": block_index,
            "paragraph_index": index,
            "table_index": 0,
            "row_index": 0,
            "cell_index": 0,
            "cell_paragraph_index": 0,
            "path": "document.p[%d]" % index,
        }
        style = paragraph.style.name if paragraph.style is not None else ""
        item = {"index": index, "text": text, "style": style, "location": location}
        paragraphs.append(item)
        blocks.append({"text": text, "style": style, "location": location})
        lines.append(text)

    for table_index, table in enumerate(document.tables, start=1):
        rows = []
        for row_index, row in enumerate(table.rows, start=1):
            row_values = []
            for cell_index, cell in enumerate(row.cells, start=1):
                cell_texts = []
                for cell_paragraph_index, paragraph in enumerate(cell.paragraphs, start=1):
                    text = trim(paragraph.text)
                    if not text:
                        continue
                    cell_texts.append(text)
                    block_index += 1
                    location = {
                        "part": "document",
                        "block_type": "table_cell",
                        "block_index": block_index,
                        "paragraph_index": 0,
                        "table_index": table_index,
                        "row_index": row_index,
                        "cell_index": cell_index,
                        "cell_paragraph_index": cell_paragraph_index,
                        "path": "document.table[%d].row[%d].cell[%d].p[%d]" % (table_index, row_index, cell_index, cell_paragraph_index),
                    }
                    blocks.append({"text": text, "style": "", "location": location})
                    lines.append(text)
                row_values.append("\n".join(cell_texts))
            rows.append(row_values)
        tables.append({"index": table_index, "rows": rows})

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
            "format": "docx",
            "source": "python_docx",
            "blocks": blocks,
            "paragraphs": paragraphs,
            "tables": tables,
            "stats": {
                "blocks": len(blocks),
                "paragraphs": len(paragraphs),
                "tables": len(tables),
                "complete": not truncated,
            }
        }
    }, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}))
