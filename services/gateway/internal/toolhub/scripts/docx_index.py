import json, re, sys
try:
    import docx
    from docx.oxml.ns import qn
    from docx.table import Table
    from docx.text.paragraph import Paragraph
except Exception:
    print("DOCX index extractor requires python-docx", file=sys.stderr)
    sys.exit(1)

# Text-only body extraction in document order for the document index:
# headings with their level, paragraphs, and one unit per table row. Block
# paths use the same document.p[n] / document.table[n].row[n] vocabulary as
# docx_read.py. Images and other assets are not read.

HEADING_STYLE = re.compile(r"^(heading|标题)\s*(\d)", re.IGNORECASE)

req = json.load(sys.stdin)

def text_of(value):
    return " ".join(str(value or "").split())

def heading_level(paragraph):
    style = paragraph.style.name if paragraph.style is not None else ""
    if style.lower() == "title":
        return 1
    match = HEADING_STYLE.match(style.strip())
    if match:
        return max(1, min(9, int(match.group(2))))
    try:
        properties = paragraph._p.pPr
        level = properties.find(qn("w:outlineLvl")) if properties is not None else None
        if level is not None:
            value = int(level.get(qn("w:val")))
            if 0 <= value <= 8:
                return value + 1
    except Exception:
        pass
    return 0

try:
    document = docx.Document(req["path"])
    units = []
    paragraph_index = 0
    table_index = 0
    for element in document.element.body.iterchildren():
        if element.tag == qn("w:p"):
            paragraph_index += 1
            paragraph = Paragraph(element, document)
            text = text_of(paragraph.text)
            if text:
                units.append({"text": text, "heading_level": heading_level(paragraph), "block": "document.p[%d]" % paragraph_index})
        elif element.tag == qn("w:tbl"):
            table_index += 1
            table = Table(element, document)
            for row_index, row in enumerate(table.rows, start=1):
                cells = []
                for cell in row.cells:
                    value = text_of(cell.text)
                    if not cells or value != cells[-1]:
                        cells.append(value)
                line = "\t".join(cells).strip()
                if line:
                    units.append({"text": line, "heading_level": 0, "block": "document.table[%d].row[%d]" % (table_index, row_index)})
    print(json.dumps({"units": units}))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
