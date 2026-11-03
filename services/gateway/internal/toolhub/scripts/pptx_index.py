import json, sys
try:
    from pptx import Presentation
except Exception:
    print("PPTX index extractor requires python-pptx", file=sys.stderr)
    sys.exit(1)

# Text-only slide extraction for the document index: one level-1 heading per
# slide (its title), then text frames, table rows and speaker notes. The
# slide number is reported as the unit's page so anchors cite slides.

req = json.load(sys.stdin)

def text_of(value):
    return " ".join(str(value or "").split())

def shape_units(shape, slide_number, block):
    units = []
    if getattr(shape, "has_text_frame", False) and shape.has_text_frame:
        for paragraph in shape.text_frame.paragraphs:
            text = text_of("".join(run.text for run in paragraph.runs) or paragraph.text)
            if text:
                units.append({"text": text, "page": slide_number, "block": block})
    if getattr(shape, "has_table", False) and shape.has_table:
        for row_index, row in enumerate(shape.table.rows, start=1):
            line = "\t".join(text_of(cell.text) for cell in row.cells).strip()
            if line:
                units.append({"text": line, "page": slide_number, "block": "%s.row[%d]" % (block, row_index)})
    if getattr(shape, "shape_type", None) is not None and hasattr(shape, "shapes"):
        for child_index, child in enumerate(shape.shapes, start=1):
            units.extend(shape_units(child, slide_number, "%s.shape[%d]" % (block, child_index)))
    return units

try:
    presentation = Presentation(req["path"])
    units = []
    for slide_number, slide in enumerate(presentation.slides, start=1):
        title = ""
        try:
            if slide.shapes.title is not None:
                title = text_of(slide.shapes.title.text)
        except Exception:
            title = ""
        body = []
        for shape_index, shape in enumerate(slide.shapes, start=1):
            if title and shape == slide.shapes.title:
                continue
            body.extend(shape_units(shape, slide_number, "presentation.slide[%d].shape[%d]" % (slide_number, shape_index)))
        if not title and body:
            title = body[0]["text"][:80]
        units.append({"text": "Slide %d: %s" % (slide_number, title or "(untitled)"), "heading_level": 1, "page": slide_number, "block": "presentation.slide[%d]" % slide_number})
        units.extend(body)
        try:
            notes = text_of(slide.notes_slide.notes_text_frame.text) if slide.has_notes_slide else ""
        except Exception:
            notes = ""
        if notes:
            units.append({"text": "Notes: " + notes, "page": slide_number, "block": "presentation.slide[%d].notes" % slide_number})
    print(json.dumps({"units": units, "pages_total": len(presentation.slides)}))
except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
