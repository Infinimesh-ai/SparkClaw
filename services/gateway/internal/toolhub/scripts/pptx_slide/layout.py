import re

from .constants import EMU_PER_PT, LINE_HEIGHT_FACTOR, MAX_STANDALONE_WRAP_LINES
from .errors import PPTXLayoutFitError
from .text import (
    apply_measured_text_flow,
    logical_text_lines,
    measured_text_flow,
    normalized_text,
    required_text_height,
    shape_font_size,
    shape_uses_multiple_lines,
    text_fits_current_flow,
    wrapped_line_count,
)


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

def horizontal_overlap(left, right):
    start = max(int(left.left), int(right.left))
    end = min(int(left.left + left.width), int(right.left + right.width))
    return max(0, end - start)

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

def shape_is_contained(background, shape, tolerance):
    return (
        int(shape.left) >= int(background.left) - tolerance
        and int(shape.top) >= int(background.top) - tolerance
        and int(shape.left + shape.width) <= int(background.left + background.width) + tolerance
        and int(shape.top + shape.height) <= int(background.top + background.height) + tolerance
    )

def derive_card_groups(slide):
    groups = []
    backgrounds = [
        (index, shape) for index, shape in enumerate(slide.shapes, start=1)
        if shape_has_fill(shape)
        and not normalized_text(getattr(shape, "text", ""))
        and int(shape.width) >= 914400
        and int(shape.height) >= 457200
    ]
    texts = [
        (index, shape) for index, shape in enumerate(slide.shapes, start=1)
        if getattr(shape, "has_text_frame", False) and normalized_text(shape.text)
    ]
    for background_index, background in backgrounds:
        tolerance = max(12700, int(min(background.width, background.height) * 0.04))
        children = [(index, shape) for index, shape in texts if shape_is_contained(background, shape, tolerance)]
        if len(children) < 2:
            continue
        title_index, title = min(children, key=lambda item: (int(item[1].top), int(item[1].left)))
        body_index, body = max(children, key=lambda item: (int(item[1].top), int(item[1].height)))
        if title_index == body_index or int(body.top) <= int(title.top):
            continue
        companions = []
        for index, shape in enumerate(slide.shapes, start=1):
            if index == background_index or normalized_text(getattr(shape, "text", "")) or not shape_has_fill(shape):
                continue
            if shape_is_contained(background, shape, tolerance):
                companions.append((index, shape))
        groups.append({
            "background_index": background_index,
            "background": background,
            "title_index": title_index,
            "title": title,
            "body_index": body_index,
            "body": body,
            "text_indexes": {index for index, _ in children},
            "companions": companions,
        })
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

def peer_card_family(groups, selected_indexes):
    selected = [group for group in groups if group["body_index"] in selected_indexes]
    if not selected:
        return []
    anchor = selected[0]
    background = anchor["background"]
    body = anchor["body"]
    tolerance = max(12700, int(min(background.width, background.height) * 0.08))
    body_left_offset = int(body.left - background.left)
    body_top_offset = int(body.top - background.top)
    family = [
        group for group in groups
        if abs(int(group["background"].top) - int(background.top)) <= tolerance
        and abs(int(group["background"].width) - int(background.width)) <= tolerance
        and abs(int(group["background"].height) - int(background.height)) <= tolerance
        and abs(int(group["body"].left - group["background"].left) - body_left_offset) <= tolerance
        and abs(int(group["body"].top - group["background"].top) - body_top_offset) <= tolerance
    ]
    ordered = sorted(family, key=lambda group: int(group["background"].left))
    for current, following in zip(ordered, ordered[1:]):
        if int(current["background"].left + current["background"].width) > int(following["background"].left) + tolerance:
            return []
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

def safe_bottom_boundary(prs, slide, shape, excluded_indexes):
    boundary = int(prs.slide_height - min(prs.slide_height * 0.05, 342900))
    current_bottom = int(shape.top + shape.height)
    for index, other in enumerate(slide.shapes, start=1):
        if index in excluded_indexes or other is shape or horizontal_overlap(shape, other) <= 0:
            continue
        other_top = int(other.top)
        if other_top >= current_bottom and other_top < boundary:
            boundary = other_top - 91440
    return max(current_bottom, boundary)

def geometry_change(index, before, after):
    return {"shape_index": index, "before": before, "after": after}

def apply_coordinated_band_layout(prs, slide, groups, selected_indexes):
    family = peer_band_family(groups, selected_indexes)
    if not family:
        return [], set()
    excluded = set()
    for group in family:
        excluded.update((group["background_index"], group["label_index"], group["body_index"]))
    common_right = min(safe_right_boundary(prs, slide, group["body"], excluded) for group in family)
    for group in family:
        background = group["background"]
        body = group["body"]
        new_background_width = int(body.left - background.left)
        new_body_width = int(common_right - body.left)
        if new_background_width <= 0 or new_body_width <= 0:
            raise ValueError("coordinated slide layout could not establish non-overlapping label and body columns")
        background.width = new_background_width
        body.width = new_body_width
    target_body_height = max(
        max(int(group["body"].height) for group in family),
        max(required_text_height(group["body"]) for group in family),
    )
    bottom_padding = max(
        int(group["background"].top + group["background"].height - group["body"].top - group["body"].height)
        for group in family
    )
    target_background_height = max(
        max(int(group["background"].height) for group in family),
        max(int(group["body"].top - group["background"].top) + target_body_height + bottom_padding for group in family),
    )
    ordered = sorted(family, key=lambda group: int(group["background"].top))
    for current, following in zip(ordered, ordered[1:]):
        if int(current["background"].top) + target_background_height > int(following["background"].top) - 91440:
            raise PPTXLayoutFitError("updated text is too tall for the coordinated peer-row layout")
    if int(ordered[-1]["background"].top) + target_background_height > safe_bottom_boundary(prs, slide, ordered[-1]["background"], excluded):
        raise PPTXLayoutFitError("updated text is too tall for the coordinated peer-row layout")
    wrapped = set()
    for group in family:
        background = group["background"]
        label = group["label"]
        body = group["body"]
        background.height = target_background_height
        body.height = target_body_height
        if int(label.height) < target_body_height:
            label.height = target_body_height
        flow = apply_measured_text_flow(body)
        if flow == "overflow":
            raise PPTXLayoutFitError("updated text does not fit the coordinated peer-row layout")
        if shape_uses_multiple_lines(body):
            wrapped.add(group["body_index"])
    return family, wrapped

def apply_coordinated_card_layout(prs, slide, groups, selected_indexes):
    family = peer_card_family(groups, selected_indexes)
    if not family:
        return [], set()
    members = set()
    for group in family:
        members.add(group["background_index"])
        members.update(group["text_indexes"])
        members.update(index for index, _ in group["companions"])
    target_body_height = max(
        max(int(group["body"].height) for group in family),
        max(required_text_height(group["body"]) for group in family),
    )
    bottom_padding = max(
        int(group["background"].top + group["background"].height - group["body"].top - group["body"].height)
        for group in family
    )
    target_background_height = max(
        max(int(group["background"].height) for group in family),
        max(int(group["body"].top - group["background"].top) + target_body_height + bottom_padding for group in family),
    )
    for group in family:
        if int(group["background"].top) + target_background_height > safe_bottom_boundary(prs, slide, group["background"], members):
            raise PPTXLayoutFitError("updated text is too tall for the coordinated card layout")
    wrapped = set()
    for group in family:
        background = group["background"]
        body = group["body"]
        original_background_top = int(background.top)
        original_background_bottom = int(background.top + background.height)
        background.height = target_background_height
        body.height = target_body_height
        flow = apply_measured_text_flow(body)
        if flow == "overflow":
            raise PPTXLayoutFitError("updated text does not fit the coordinated card layout")
        if shape_uses_multiple_lines(body):
            wrapped.add(group["body_index"])
        tolerance = max(12700, int(min(background.width, background.height) * 0.04))
        for _, companion in group["companions"]:
            top_aligned = abs(int(companion.top) - original_background_top) <= tolerance
            bottom_aligned = abs(int(companion.top + companion.height) - original_background_bottom) <= tolerance
            if top_aligned and bottom_aligned:
                companion.height = int(background.top + background.height - companion.top)
    return family, wrapped

def fit_shape_without_collision(prs, slide, shape_index, excluded_indexes):
    shape = slide.shapes[shape_index - 1]
    original_height = int(shape.height)
    explicit_breaks = len(logical_text_lines(shape.text_frame.text)) > 1
    flow = measured_text_flow(shape)
    if flow != "overflow":
        if flow == "wrapped":
            shape.text_frame.word_wrap = True
        return flow
    if not explicit_breaks:
        right = safe_right_boundary(prs, slide, shape, excluded_indexes)
        if right > int(shape.left + shape.width):
            shape.width = int(right - shape.left)
        flow = measured_text_flow(shape)
        if flow == "single":
            shape.text_frame.word_wrap = False
            return flow
        if flow == "wrapped":
            shape.text_frame.word_wrap = True
            return flow
    line_count = wrapped_line_count(shape)
    size_pt = shape_font_size(shape)
    text_frame = shape.text_frame
    original_usable_height_pt = max(
        0.0,
        (original_height - text_frame.margin_top - text_frame.margin_bottom) / EMU_PER_PT,
    )
    original_line_capacity = int(original_usable_height_pt // max(size_pt * LINE_HEIGHT_FACTOR, 1.0))
    if line_count > max(MAX_STANDALONE_WRAP_LINES, original_line_capacity):
        raise PPTXLayoutFitError("updated text is too long for its slide shape after multi-line layout; shorten the text")
    required_height = required_text_height(shape)
    bottom = safe_bottom_boundary(prs, slide, shape, excluded_indexes)
    if int(shape.top) + required_height > bottom:
        raise PPTXLayoutFitError("updated text is too tall for its slide shape without overlapping nearby content")
    shape.height = required_height
    shape.text_frame.word_wrap = True
    if not text_fits_current_flow(shape):
        raise PPTXLayoutFitError("updated text does not fit its slide shape after multi-line layout")
    return "wrapped"

def validate_slide_layout(prs, slide, updated_indexes, band_groups, card_groups, before):
    for index, shape in enumerate(slide.shapes, start=1):
        if int(shape.left) < 0 or int(shape.top) < 0 or int(shape.left + shape.width) > int(prs.slide_width) or int(shape.top + shape.height) > int(prs.slide_height):
            raise ValueError("slide shape %s is outside the presentation canvas" % index)
    for index in updated_indexes:
        shape = slide.shapes[index - 1]
        if not text_fits_current_flow(shape):
            raise PPTXLayoutFitError("updated text does not fit slide shape %s" % index)
    for group in band_groups:
        background = group["background"]
        body = group["body"]
        if int(background.left + background.width) > int(body.left):
            raise ValueError("coordinated label background overlaps body shape %s" % group["body_index"])
        if int(body.top + body.height) > int(background.top + background.height) + 12700:
            raise ValueError("coordinated peer-row body extends below its background shape %s" % group["background_index"])
    for group in card_groups:
        background = group["background"]
        body = group["body"]
        if not shape_is_contained(background, body, 12700):
            raise ValueError("coordinated card body extends beyond background shape %s" % group["background_index"])
    peer_font_uniform = True
    for family in (band_groups, card_groups):
        if not family:
            continue
        body_heights = {int(group["body"].height) for group in family}
        body_fonts = {round(shape_font_size(group["body"]), 2) for group in family}
        if len(body_heights) != 1:
            raise ValueError("coordinated peer text boxes are not geometrically uniform")
        if len(body_fonts) != 1:
            peer_font_uniform = False
        for group in family:
            body_index = group["body_index"]
            if round(shape_font_size(group["body"]), 2) != before[body_index]["font_size_pt"]:
                raise ValueError("coordinated peer body font size changed")
    return {
        "updated_text_fits": True,
        "wrapped_text_fits": True,
        "canvas_bounds": True,
        "companion_non_overlap": True,
        "peer_font_uniform": peer_font_uniform,
        "peer_font_preserved": True,
        "peer_geometry_uniform": True,
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
