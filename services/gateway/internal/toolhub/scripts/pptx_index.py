import json, sys
try:
    from pptx import Presentation
except Exception:
    print("PPTX index extractor requires python-pptx", file=sys.stderr)
    sys.exit(1)

# Text-only slide extraction for the document index: one level-1 heading per
# slide (its title), then text frames, table rows and speaker notes. The
# slide number is reported as the unit's page so anchors cite slides.

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

def shape_units(shape, slide_number, block):
    units = BoundedUnits()
    if getattr(shape, "has_text_frame", False) and shape.has_text_frame:
        for paragraph_index, paragraph in enumerate(shape.text_frame.paragraphs, start=1):
            text = text_of("".join(run.text for run in paragraph.runs) or paragraph.text)
            if text:
                units.append({"text": text, "page": slide_number, "block": "%s.p[%d]" % (block, paragraph_index)})
    if getattr(shape, "has_table", False) and shape.has_table:
        for row_index, row in enumerate(shape.table.rows, start=1):
            line = "\t".join(text_of(cell.text) for cell in row.cells)
            units.append({"text": line, "kind": "table_row", "values": [{"column": column, "text": text_of(cell.text), "is_merge_origin": cell.is_merge_origin, "is_spanned": cell.is_spanned} for column, cell in enumerate(row.cells, start=1)], "dependencies": ["%s.row[1]" % block] if row_index > 1 else [], "page": slide_number, "block": "%s.row[%d]" % (block, row_index)})
    if getattr(shape, "shape_type", None) is not None and hasattr(shape, "shapes"):
        for child_index, child in enumerate(shape.shapes, start=1):
            units.extend(shape_units(child, slide_number, "%s.shape[%d]" % (block, child_index)))
    if not units and not hasattr(shape, "shapes"):
        units.append({"text": "", "page": slide_number, "block": block, "kind": "object", "availability": "unsupported", "reason": "non_text_shape_requires_visual_analysis"})
    return units

try:
    presentation = Presentation(req["path"])
    units = BoundedUnits()
    for slide_number, slide in enumerate(presentation.slides, start=1):
        title = ""
        try:
            if slide.shapes.title is not None:
                title = text_of(slide.shapes.title.text)
        except Exception:
            title = ""
        body = BoundedUnits()
        for shape_index, shape in enumerate(slide.shapes, start=1):
            if title and shape == slide.shapes.title:
                continue
            body.extend(shape_units(shape, slide_number, "presentation.slide[%d].shape[%d]" % (slide_number, shape_index)))
        units.append({"text": title, "title": title or "Slide %d" % slide_number, "kind": "slide", "heading_level": 1, "page": slide_number, "block": "presentation.slide[%d]" % slide_number})
        units.extend(body)
        try:
            notes = text_of(slide.notes_slide.notes_text_frame.text) if slide.has_notes_slide else ""
        except Exception:
            notes = ""
        if notes:
            units.append({"text": notes, "kind": "notes", "page": slide_number, "block": "presentation.slide[%d].notes" % slide_number})
    print(json.dumps({"units": units, "pages_total": len(presentation.slides), "limitations": ["Theme, layout, animation, chart values and visual reading order are not fully extracted."]}))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
