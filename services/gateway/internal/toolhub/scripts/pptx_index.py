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
structure_v2 = req.get("structure_version", 1) >= 2

def text_of(value):
    return str(value if value is not None else "")

def section_outline(presentation):
    # Native sections refer to presentation slide IDs, not slide part names
    # or shape IDs. Read the actual extension XML; python-pptx has no section API.
    p = "http://schemas.openxmlformats.org/presentationml/2006/main"
    namespaces = (p, "http://schemas.microsoft.com/office/powerpoint/2010/main")
    slide_ids = presentation._element.find("{%s}sldIdLst" % p)
    pages = {item.get("id"): page for page, item in enumerate(slide_ids, start=1)} if slide_ids is not None else {}
    outline = BoundedUnits()
    for namespace in namespaces:
        for sections in presentation._element.iter("{%s}sectionLst" % namespace):
            for section in sections.findall("{%s}section" % namespace):
                title = section.get("name", "")
                members = section.find("{%s}sldIdLst" % namespace)
                if not title.strip() or members is None:
                    continue
                section_pages = [pages[item.get("id")] for item in members.findall("{%s}sldId" % namespace) if item.get("id") in pages]
                if section_pages:
                    outline.append({"title": title, "level": 1, "page": min(section_pages)})
    outline.sort(key=lambda entry: entry["page"])
    return outline

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
        unit = {"text": title, "title": title or "Slide %d" % slide_number, "kind": "slide", "heading_level": 1, "page": slide_number, "block": "presentation.slide[%d]" % slide_number}
        if structure_v2:
            unit["structure_source"] = "slide"
        units.append(unit)
        units.extend(body)
        try:
            notes = text_of(slide.notes_slide.notes_text_frame.text) if slide.has_notes_slide else ""
        except Exception:
            notes = ""
        if notes:
            units.append({"text": notes, "kind": "notes", "page": slide_number, "block": "presentation.slide[%d].notes" % slide_number})
    output = {"units": units, "pages_total": len(presentation.slides), "limitations": ["Theme, layout, animation, chart values and visual reading order are not fully extracted."]}
    if structure_v2:
        outline = section_outline(presentation)
        if len(units) + len(outline) > 250000 or units.bytes + outline.bytes > (64 << 20):
            raise ValueError("Document exceeds structure extraction admission")
        output["outline"] = outline
    print(json.dumps(output))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
