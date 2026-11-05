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

class BoundedUnits(list):
    def __init__(self):
        super().__init__()
        self.bytes = 0
    def append(self, unit):
        self.bytes += len(json.dumps(unit, ensure_ascii=False).encode("utf-8"))
        if len(self) >= 250000 or self.bytes > (64 << 20):
            raise ValueError("Document exceeds structure extraction admission")
        super().append(unit)
    def extend(self, units):
        for unit in units:
            self.append(unit)

req = json.load(sys.stdin)

def text_of(value):
    return str(value if value is not None else "")

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
    units = BoundedUnits()
    paragraph_index = 0
    table_index = 0
    for element in document.element.body.iterchildren():
        if element.tag == qn("w:p"):
            paragraph_index += 1
            paragraph = Paragraph(element, document)
            text = text_of(paragraph.text)
            if text or heading_level(paragraph):
                units.append({"text": text, "heading_level": heading_level(paragraph), "block": "document.p[%d]" % paragraph_index})
        elif element.tag == qn("w:tbl"):
            table_index += 1
            table = Table(element, document)
            header_block = "document.table[%d].row[1]" % table_index
            anchors = {}
            for row_index, row in enumerate(table.rows, start=1):
                cells, values = [], []
                dependencies = {header_block} if row_index > 1 else set()
                for column, cell in enumerate(row.cells, start=1):
                    key = cell._tc
                    anchor_row, anchor = anchors.setdefault(key, (row_index, column))
                    alias = (anchor_row, anchor) != (row_index, column)
                    if alias and anchor_row != row_index:
                        dependencies.add("document.table[%d].row[%d]" % (table_index, anchor_row))
                    value = text_of(cell.text)
                    cells.append(value if not alias else "")
                    values.append({"column": column, "text": value if not alias else "", "merge_anchor_column": anchor, "merge_anchor_row": anchor_row, "is_merge_alias": alias})
                units.append({"text": "\t".join(cells), "kind": "table_row", "values": values,
                    "dependencies": sorted(dependencies),
                    "block": "document.table[%d].row[%d]" % (table_index, row_index)})
    for index, shape in enumerate(document.inline_shapes, start=1):
        units.append({"text": "", "kind": "image", "block": "document.inline_shape[%d]" % index,
            "availability": "unsupported", "reason": "visual_content_requires_separate_analysis"})
    print(json.dumps({"units": units, "limitations": ["Nested tables, headers, footers, comments, tracked changes, drawing layout and embedded objects are not fully extracted."]}))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
