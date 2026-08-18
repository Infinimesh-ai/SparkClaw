import math
import re

from .constants import EMU_PER_PT, LINE_HEIGHT_FACTOR, WRAP_WIDTH_FACTOR


def normalized_text(value):
    return " ".join(str(value or "").split())

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
    return min(max_size_pt, usable_width_pt / visual_text_units(text) * 0.94)

def shape_font_size(shape):
    text_frame = shape.text_frame
    runs = [run for paragraph in text_frame.paragraphs for run in paragraph.runs if run.text]
    explicit_sizes = [run.font.size.pt for run in runs if run.font.size is not None]
    return max(explicit_sizes) if explicit_sizes else 18.0

def text_fits_single_line(shape, size_pt=None):
    if size_pt is None:
        size_pt = shape_font_size(shape)
    return fitted_single_line_size(shape, shape.text_frame.text, size_pt) >= size_pt - 0.25

def logical_text_lines(text):
    return re.split(r"[\r\n\v]", str(text or ""))

def text_line_capacity(shape, size_pt=None):
    if size_pt is None:
        size_pt = shape_font_size(shape)
    text_frame = shape.text_frame
    usable_width_pt = max(
        1.0,
        (shape.width - text_frame.margin_left - text_frame.margin_right) / EMU_PER_PT,
    )
    return max(1.0, usable_width_pt / max(size_pt, 1.0) * WRAP_WIDTH_FACTOR)

def wrapped_line_count(shape, text=None, size_pt=None):
    if text is None:
        text = shape.text_frame.text
    capacity = text_line_capacity(shape, size_pt)
    return max(1, sum(max(1, int(math.ceil(visual_text_units(line) / capacity))) for line in logical_text_lines(text)))

def required_text_height(shape, text=None, size_pt=None):
    if size_pt is None:
        size_pt = shape_font_size(shape)
    text_frame = shape.text_frame
    margins_pt = (text_frame.margin_top + text_frame.margin_bottom) / EMU_PER_PT
    lines = wrapped_line_count(shape, text, size_pt)
    return int(math.ceil((margins_pt + lines * size_pt * LINE_HEIGHT_FACTOR) * EMU_PER_PT))

def text_fits_wrapped(shape, size_pt=None):
    return required_text_height(shape, size_pt=size_pt) <= int(shape.height)

def measured_text_flow(shape, size_pt=None):
    explicit_breaks = len(logical_text_lines(shape.text_frame.text)) > 1
    if not explicit_breaks and text_fits_single_line(shape, size_pt):
        return "single"
    if text_fits_wrapped(shape, size_pt):
        return "wrapped"
    return "overflow"

def apply_measured_text_flow(shape, size_pt=None):
    flow = measured_text_flow(shape, size_pt)
    if flow == "single":
        return flow
    shape.text_frame.word_wrap = True
    return flow

def text_fits_current_flow(shape, size_pt=None):
    if size_pt is None:
        size_pt = shape_font_size(shape)
    lines = logical_text_lines(shape.text_frame.text)
    if len(lines) == 1 and text_fits_single_line(shape, size_pt):
        return True
    if shape.text_frame.word_wrap is False:
        if any(fitted_single_line_size(shape, line, size_pt) < size_pt - 0.25 for line in lines):
            return False
        return required_text_height(shape, size_pt=size_pt) <= int(shape.height)
    return text_fits_wrapped(shape, size_pt)

def shape_uses_multiple_lines(shape, size_pt=None):
    return len(logical_text_lines(shape.text_frame.text)) > 1 or wrapped_line_count(shape, size_pt=size_pt) > 1
