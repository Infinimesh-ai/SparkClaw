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
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape != slide.shapes.title:
                shape.text = body
                return
