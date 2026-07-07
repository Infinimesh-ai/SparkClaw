
import copy
import json
import os
import sys
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX slide adapter requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
op = req.get("operation")

def positive_index(value, name):
    idx = int(value or 0)
    if idx <= 0:
        raise ValueError("%s must be a positive 1-based integer" % name)
    return idx

def slide_at(prs, idx):
    if idx < 1 or idx > len(prs.slides):
        raise ValueError("slide_index out of range: %s" % idx)
    return prs.slides[idx - 1]

def delete_slide(prs, idx):
    slide = slide_at(prs, idx)
    slide_id_list = prs.slides._sldIdLst
    slide_id = slide_id_list[idx - 1]
    rel_id = slide_id.rId
    slide_id_list.remove(slide_id)
    prs.part.drop_rel(rel_id)

def fill_text_placeholders(slide, title, body):
    title = str(title or "")
    body = str(body or "")
    if title and slide.shapes.title is not None:
        slide.shapes.title.text = title
    if body:
        for shape in slide.placeholders:
            if shape == slide.shapes.title:
                continue
            if hasattr(shape, "text_frame"):
                shape.text = body
                return
        left = top = width = height = None
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape != slide.shapes.title:
                shape.text = body
                return

def duplicate_slide(prs, idx):
    source = slide_at(prs, idx)
    blank_layout = prs.slide_layouts[6] if len(prs.slide_layouts) > 6 else prs.slide_layouts[0]
    dest = prs.slides.add_slide(blank_layout)
    for shape in source.shapes:
        dest.shapes._spTree.insert_element_before(copy.deepcopy(shape.element), 'p:extLst')
    for rel in source.part.rels.values():
        if "notesSlide" in rel.reltype:
            continue
        if rel.is_external:
            dest.part.rels.get_or_add_ext_rel(rel.reltype, rel.target_ref)
        else:
            dest.part.rels.get_or_add(rel.reltype, rel._target)
    slide_id_list = prs.slides._sldIdLst
    new_slide_id = slide_id_list[-1]
    slide_id_list.remove(new_slide_id)
    slide_id_list.insert(idx, new_slide_id)
    return dest

try:
    prs = Presentation(req["path"])
    result = {
        "status": "pptx_version_written",
        "operation": op,
        "path": req["path"],
        "output_path": req["output_path"]
    }
    if op == "add_slide":
        layout_index = int(req.get("layout_index") or 0)
        if layout_index < 0 or layout_index >= len(prs.slide_layouts):
            raise ValueError("layout_index out of range: %s" % layout_index)
        slide = prs.slides.add_slide(prs.slide_layouts[layout_index])
        fill_text_placeholders(slide, req.get("title"), req.get("body"))
        result["slide_index"] = len(prs.slides)
        result["layout_index"] = layout_index
        result["title"] = str(req.get("title") or "")
        result["body"] = str(req.get("body") or "")
    elif op == "duplicate_slide":
        idx = positive_index(req.get("slide_index"), "slide_index")
        duplicate_slide(prs, idx)
        result["slide_index"] = idx
        result["inserted_slide_index"] = idx + 1
    elif op == "delete_slide":
        idx = positive_index(req.get("slide_index"), "slide_index")
        if len(prs.slides) <= 1:
            raise ValueError("cannot delete the only slide")
        delete_slide(prs, idx)
        result["slide_index"] = idx
    else:
        raise ValueError("unsupported pptx operation: %s" % op)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    prs.save(req["output_path"])
    result["slides"] = len(prs.slides)
    result["bytes"] = os.path.getsize(req["output_path"])
    print(json.dumps(result, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}, ensure_ascii=False))
