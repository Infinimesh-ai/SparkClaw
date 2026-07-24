
import copy
import json
import os
import re
import sys
try:
    from pptx import Presentation
    from pptx.util import Pt
    from PIL import ImageFont
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
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape != slide.shapes.title:
                shape.text = body
                return

def normalized_text(value):
    return " ".join(str(value or "").split())

def metric_font_path():
    windows = os.environ.get("WINDIR", "")
    candidates = [
        os.environ.get("SPARKCLAW_PPTX_METRIC_FONT", ""),
        "/System/Library/Fonts/Hiragino Sans GB.ttc",
        "/System/Library/Fonts/PingFang.ttc",
        "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        os.path.join(windows, "Fonts", "msyh.ttc") if windows else "",
        os.path.join(windows, "Fonts", "arial.ttf") if windows else "",
    ]
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return candidate
    return ""

def visual_text_units(text):
    units = 0.0
    for char in text:
        code = ord(char)
        if char.isspace():
            units += 0.35
        elif code >= 0x2E80:
            units += 1.0
        elif char.isupper():
            units += 0.7
        elif char.islower() or char.isdigit():
            units += 0.55
        else:
            units += 0.5
    return max(units, 1.0)

def fitted_single_line_size(shape, text, max_size_pt):
    text_frame = shape.text_frame
    usable_width_pt = max(
        1.0,
        (shape.width - text_frame.margin_left - text_frame.margin_right) / 12700.0,
    )
    # System fonts are not interchangeable: fallback Latin fonts often render
    # CJK text as narrow missing-glyph boxes. Keep CJK layout decisions
    # deterministic across runners instead of trusting those metrics.
    font_path = metric_font_path()
    has_cjk = any(ord(char) >= 0x2E80 for char in text)
    if font_path and not has_cjk:
        try:
            metric_font = ImageFont.truetype(font_path, 100, index=0)
            width_at_100 = float(metric_font.getlength(text))
            if width_at_100 > 0:
                return min(max_size_pt, usable_width_pt * 100.0 / width_at_100 * 0.94)
        except Exception:
            pass
    return min(max_size_pt, usable_width_pt / visual_text_units(text) * 0.94)

def shape_font_size(shape):
    text_frame = shape.text_frame
    runs = [run for paragraph in text_frame.paragraphs for run in paragraph.runs if run.text]
    explicit_sizes = [run.font.size.pt for run in runs if run.font.size is not None]
    return max(explicit_sizes) if explicit_sizes else 18.0

def set_shape_font_size(shape, size_pt):
    runs = [run for paragraph in shape.text_frame.paragraphs for run in paragraph.runs if run.text]
    for run in runs:
        run.font.size = Pt(size_pt)

def text_fits_single_line(shape, size_pt=None):
    if size_pt is None:
        size_pt = shape_font_size(shape)
    return fitted_single_line_size(shape, shape.text_frame.text, size_pt) >= size_pt - 0.25

def shape_bounds(shape):
    word_wrap = None
    if getattr(shape, "has_text_frame", False):
        word_wrap = shape.text_frame.word_wrap
    return {
        "x": int(shape.left),
        "y": int(shape.top),
        "width": int(shape.width),
        "height": int(shape.height),
        "font_size_pt": round(shape_font_size(shape), 2) if getattr(shape, "has_text_frame", False) and normalized_text(shape.text) else None,
        "word_wrap": bool(word_wrap) if word_wrap is not None else None,
    }

def vertical_overlap(left, right):
    top = max(int(left.top), int(right.top))
    bottom = min(int(left.top + left.height), int(right.top + right.height))
    return max(0, bottom - top)

def shape_has_fill(shape):
    try:
        return shape.fill.type is not None
    except Exception:
        return False

def derive_band_groups(slide):
    groups = []
    used = set()
    backgrounds = [
        (index, shape) for index, shape in enumerate(slide.shapes, start=1)
        if shape_has_fill(shape) and not normalized_text(getattr(shape, "text", "")) and int(shape.width) > 0
    ]
    texts = [
        (index, shape) for index, shape in enumerate(slide.shapes, start=1)
        if getattr(shape, "has_text_frame", False) and normalized_text(shape.text)
    ]
    for background_index, background in backgrounds:
        bg_left = int(background.left)
        bg_right = int(background.left + background.width)
        bg_center = int(background.top + background.height / 2)
        row_texts = [
            (index, shape) for index, shape in texts
            if index not in used
            and int(shape.top) <= bg_center <= int(shape.top + shape.height)
            and vertical_overlap(background, shape) >= min(int(background.height), int(shape.height)) * 0.45
        ]
        labels = [
            (index, shape) for index, shape in row_texts
            if int(shape.left) >= bg_left - int(background.width) * 0.03
            and int(shape.left + shape.width) <= bg_right + int(background.width) * 0.03
        ]
        if not labels:
            continue
        label_index, label = min(labels, key=lambda item: int(item[1].left + item[1].width / 2))
        bodies = [
            (index, shape) for index, shape in row_texts
            if index != label_index
            and int(shape.left) > int(label.left)
            and int(shape.left) <= bg_right + int(background.width) * 0.03
            and int(shape.left + shape.width) > bg_right + int(background.width) * 0.20
        ]
        if not bodies:
            continue
        body_index, body = min(bodies, key=lambda item: abs(int(item[1].top + item[1].height / 2) - bg_center))
        groups.append({
            "background_index": background_index,
            "background": background,
            "label_index": label_index,
            "label": label,
            "body_index": body_index,
            "body": body,
        })
        used.update((background_index, label_index, body_index))
    return groups

def peer_band_family(groups, selected_indexes):
    selected = [group for group in groups if group["body_index"] in selected_indexes]
    if not selected:
        return []
    anchor = selected[0]
    tolerance = max(12700, int(anchor["background"].width * 0.04))
    family = [
        group for group in groups
        if abs(int(group["background"].left) - int(anchor["background"].left)) <= tolerance
        and abs(int(group["background"].width) - int(anchor["background"].width)) <= tolerance
        and abs(int(group["body"].left) - int(anchor["body"].left)) <= tolerance
    ]
    return family if len(family) >= 2 else []

def safe_right_boundary(prs, slide, shape, excluded_indexes):
    boundary = int(prs.slide_width - min(prs.slide_width * 0.05, 457200))
    current_right = int(shape.left + shape.width)
    for index, other in enumerate(slide.shapes, start=1):
        if index in excluded_indexes or other is shape or vertical_overlap(shape, other) <= 0:
            continue
        other_left = int(other.left)
        if other_left >= current_right and other_left < boundary:
            boundary = other_left - 91440
    return max(current_right, boundary)

def geometry_change(index, before, after):
    return {"shape_index": index, "before": before, "after": after}

def apply_coordinated_band_layout(prs, slide, groups, selected_indexes):
    family = peer_band_family(groups, selected_indexes)
    if not family:
        return set()
    excluded = set()
    for group in family:
        excluded.update((group["background_index"], group["label_index"], group["body_index"]))
    common_right = min(safe_right_boundary(prs, slide, group["body"], excluded) for group in family)
    body_font_sizes = [shape_font_size(group["body"]) for group in family]
    common_font_size = min(body_font_sizes)
    if max(body_font_sizes) - min(body_font_sizes) > 1.0:
        raise ValueError("coordinated slide layout rejected inconsistent peer body font sizes")
    adjusted = set()
    for group in family:
        background = group["background"]
        body = group["body"]
        new_background_width = int(body.left - background.left)
        new_body_width = int(common_right - body.left)
        if new_background_width <= 0 or new_body_width <= 0:
            raise ValueError("coordinated slide layout could not establish non-overlapping label and body columns")
        if int(background.width) != new_background_width:
            background.width = new_background_width
            adjusted.add(group["background_index"])
        if int(body.width) != new_body_width:
            body.width = new_body_width
            adjusted.add(group["body_index"])
        set_shape_font_size(body, common_font_size)
        body.text_frame.word_wrap = False
        if not text_fits_single_line(body, common_font_size):
            raise ValueError("updated text is too long for the coordinated slide layout; shorten the text")
    return adjusted

def expand_shape_without_collision(prs, slide, shape_index, excluded_indexes):
    shape = slide.shapes[shape_index - 1]
    if text_fits_single_line(shape):
        return False
    right = safe_right_boundary(prs, slide, shape, excluded_indexes)
    if right > int(shape.left + shape.width):
        shape.width = int(right - shape.left)
    shape.text_frame.word_wrap = False
    if not text_fits_single_line(shape):
        raise ValueError("updated text is too long for its slide shape; shorten the text")
    return True

def validate_slide_layout(prs, slide, updated_indexes, band_groups):
    for index, shape in enumerate(slide.shapes, start=1):
        if int(shape.left) < 0 or int(shape.top) < 0 or int(shape.left + shape.width) > int(prs.slide_width) or int(shape.top + shape.height) > int(prs.slide_height):
            raise ValueError("slide shape %s is outside the presentation canvas" % index)
    for index in updated_indexes:
        shape = slide.shapes[index - 1]
        if not text_fits_single_line(shape):
            raise ValueError("updated text does not fit slide shape %s" % index)
    for group in band_groups:
        background = group["background"]
        body = group["body"]
        if int(background.left + background.width) > int(body.left):
            raise ValueError("coordinated label background overlaps body shape %s" % group["body_index"])
    return {
        "updated_text_fits": True,
        "canvas_bounds": True,
        "companion_non_overlap": True,
        "peer_font_uniform": True,
    }

def page_marker_warnings(prs):
    warnings = []
    marker_pattern = re.compile(r"(?<!\d)(\d+)\s*/\s*(\d+)(?!\d)")
    for slide_index, slide in enumerate(prs.slides, start=1):
        for shape in slide.shapes:
            if not getattr(shape, "has_text_frame", False):
                continue
            if int(shape.top) < int(prs.slide_height * 0.75):
                continue
            value = normalized_text(shape.text)
            match = marker_pattern.search(value)
            if not match:
                continue
            current, declared_total = int(match.group(1)), int(match.group(2))
            if current != slide_index or declared_total != len(prs.slides):
                warnings.append(
                    "slide %d page marker %d/%d does not match physical position %d/%d"
                    % (slide_index, current, declared_total, slide_index, len(prs.slides))
                )
    return warnings

def replace_text_preserving_style(shape, text):
    text = str(text or "")
    if not text.strip():
        raise ValueError("updated shape text must not be empty")
    if "\n" in text or "\r" in text:
        raise ValueError("updated shape text must be one text block without explicit line breaks")
    text_frame = shape.text_frame
    paragraph = text_frame.paragraphs[0]
    if paragraph.runs:
        paragraph.runs[0].text = text
        for run in paragraph.runs[1:]:
            run.text = ""
    else:
        paragraph.add_run().text = text
    for extra_paragraph in text_frame.paragraphs[1:]:
        for run in extra_paragraph.runs:
            run.text = ""

def update_slide(prs, slide, updates, layout_policy):
    if not isinstance(updates, list) or not updates:
        raise ValueError("updates must be a non-empty array")
    layout_policy = normalized_text(layout_policy or "coordinated").lower()
    if layout_policy not in ("preserve", "coordinated"):
        raise ValueError("layout_policy must be preserve or coordinated")
    seen = set()
    before = {index: shape_bounds(shape) for index, shape in enumerate(slide.shapes, start=1)}
    for update in updates:
        if not isinstance(update, dict):
            raise ValueError("each slide update must be an object")
        shape_index = positive_index(update.get("shape_index"), "shape_index")
        if shape_index in seen:
            raise ValueError("shape_index is duplicated: %s" % shape_index)
        seen.add(shape_index)
        if shape_index > len(slide.shapes):
            raise ValueError("shape_index out of range: %s" % shape_index)
        shape = slide.shapes[shape_index - 1]
        if not getattr(shape, "has_text_frame", False):
            raise ValueError("shape_index does not identify a text shape: %s" % shape_index)
        expected = normalized_text(update.get("old_text"))
        current = normalized_text(shape.text)
        if not expected or current != expected:
            raise ValueError("old_text does not match slide shape %s" % shape_index)
        replace_text_preserving_style(shape, update.get("text"))

    groups = derive_band_groups(slide)
    adjusted = set()
    coordinated_family = []
    if layout_policy == "coordinated":
        coordinated_family = peer_band_family(groups, seen)
        adjusted.update(apply_coordinated_band_layout(prs, slide, groups, seen))
        family_body_indexes = {group["body_index"] for group in coordinated_family}
        excluded = set()
        for group in coordinated_family:
            excluded.update((group["background_index"], group["label_index"], group["body_index"]))
        for shape_index in seen - family_body_indexes:
            if expand_shape_without_collision(prs, slide, shape_index, excluded | seen):
                adjusted.add(shape_index)
    else:
        for shape_index in seen:
            shape = slide.shapes[shape_index - 1]
            if not text_fits_single_line(shape):
                raise ValueError("updated text is too long for preserve layout_policy; use coordinated or shorten the text")

    checks = validate_slide_layout(prs, slide, seen, coordinated_family)
    changes = []
    for shape_index in sorted(adjusted):
        after = shape_bounds(slide.shapes[shape_index - 1])
        if before[shape_index] != after:
            changes.append(geometry_change(shape_index, before[shape_index], after))
    adjusted = {change["shape_index"] for change in changes}
    return {
        "updated_shapes": len(seen),
        "fitted_shapes": 0,
        "layout_policy": layout_policy,
        "layout_adjusted_shapes": len(adjusted),
        "layout_adjusted_shape_indexes": sorted(adjusted),
        "layout_changes": changes,
        "layout_checks": checks,
        "companion_groups_used": len(coordinated_family),
    }

def duplicate_slide(prs, idx):
    source = slide_at(prs, idx)
    dest = prs.slides.add_slide(source.slide_layout)
    for shape in source.shapes:
        dest.shapes._spTree.insert_element_before(copy.deepcopy(shape.element), 'p:extLst')
    for rel in source.part.rels.values():
        if "notesSlide" in rel.reltype or "slideLayout" in rel.reltype:
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
    elif op == "update_slide":
        idx = positive_index(req.get("slide_index"), "slide_index")
        slide = slide_at(prs, idx)
        result.update(update_slide(prs, slide, req.get("updates"), req.get("layout_policy")))
        result["warnings"] = page_marker_warnings(prs)
        result["slide_index"] = idx
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
