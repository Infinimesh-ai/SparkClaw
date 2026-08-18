from .errors import PPTXLayoutFitError
from .layout import (
    apply_coordinated_band_layout,
    apply_coordinated_card_layout,
    derive_band_groups,
    derive_card_groups,
    fit_shape_without_collision,
    geometry_change,
    shape_bounds,
    validate_slide_layout,
)
from .slides import positive_index
from .text import normalized_text, shape_uses_multiple_lines, text_fits_current_flow
from .text_edit import replace_text_preserving_style


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
        replace_text_preserving_style(shape, update)

    band_groups = derive_band_groups(slide)
    card_groups = derive_card_groups(slide)
    coordinated_bands = []
    coordinated_cards = []
    wrapped = set()
    if layout_policy == "coordinated":
        coordinated_bands, band_wrapped = apply_coordinated_band_layout(prs, slide, band_groups, seen)
        coordinated_cards, card_wrapped = apply_coordinated_card_layout(prs, slide, card_groups, seen)
        wrapped.update(band_wrapped)
        wrapped.update(card_wrapped)
        family_body_indexes = {
            group["body_index"] for group in coordinated_bands + coordinated_cards
        }
        excluded = set()
        for group in coordinated_bands:
            excluded.update((group["background_index"], group["label_index"], group["body_index"]))
        for group in coordinated_cards:
            excluded.add(group["background_index"])
            excluded.update(group["text_indexes"])
            excluded.update(index for index, _ in group["companions"])
        for shape_index in seen - family_body_indexes:
            flow = fit_shape_without_collision(prs, slide, shape_index, excluded)
            if flow == "wrapped":
                wrapped.add(shape_index)
    else:
        for shape_index in seen:
            shape = slide.shapes[shape_index - 1]
            if not text_fits_current_flow(shape):
                raise PPTXLayoutFitError("updated text does not fit preserve layout_policy; use coordinated or shorten the text")
            if shape_uses_multiple_lines(shape):
                wrapped.add(shape_index)

    checks = validate_slide_layout(prs, slide, seen, coordinated_bands, coordinated_cards, before)
    changes = []
    for shape_index, shape in enumerate(slide.shapes, start=1):
        after = shape_bounds(slide.shapes[shape_index - 1])
        if before[shape_index] != after:
            changes.append(geometry_change(shape_index, before[shape_index], after))
    adjusted = {change["shape_index"] for change in changes}
    return {
        "updated_shapes": len(seen),
        "fitted_shapes": 0,
        "wrapped_shapes": len(wrapped),
        "wrapped_shape_indexes": sorted(wrapped),
        "layout_policy": layout_policy,
        "layout_adjusted_shapes": len(adjusted),
        "layout_adjusted_shape_indexes": sorted(adjusted),
        "layout_changes": changes,
        "layout_checks": checks,
        "companion_groups_used": len(coordinated_bands) + len(coordinated_cards),
    }
