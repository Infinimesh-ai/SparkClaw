#!/usr/bin/env python3
"""Adversarial source-location and slide-visual fixtures; never overwrite files."""
import argparse
import io
import json
from pathlib import Path

from docx import Document
from PIL import Image, ImageDraw, ImageFont
from pptx import Presentation
from pptx.enum.shapes import MSO_SHAPE
from pptx.util import Inches, Pt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("destination", type=Path)
    root = parser.parse_args().destination
    root.mkdir(parents=True, exist_ok=True)
    for name in ("chapter-order.docx", "chapter-before.docx", "visual-relations.pptx", "visual-reversed.pptx", "location-visual-ground-truth.json"):
        if (root / name).exists():
            raise SystemExit(f"refusing to overwrite {name}")
    doc = Document()
    for title, level, body in [
        ("第21节：上线前准备", 1, "这里只讨论上线准备。后文还有附录和复核，章节编号不是阅读顺序编号。"),
        ("最后的自我检查", 2, "旧版清单：检查备份。"),
        ("附录甲：工具限制", 1, "工具 Orbit 仅适用于隔离环境，不能用于生产。"),
        ("附录乙：样例", 1, "样例不是已完成任务的证明。"),
        ("快速复习与收尾", 1, "这一部分在两份附录之后。"),
        ("最后的自我检查", 2, "新版清单：核对授权。"),
    ]:
        doc.add_heading(title, level)
        doc.add_paragraph(body)
    doc.add_paragraph("新版清单：检查证据。")
    doc.add_paragraph("新版清单：先实现采集层。")
    doc.save(root / "chapter-order.docx")
    variant = Document()
    for title, level, body in [
        ("收尾检查（无章节编号）", 1, "本节没有数字章节编号。"),
        ("最后的自我检查", 2, "检查设备断电。"),
        ("附录九：历史说明", 1, "第21节是旧版本的叫法，不能用作本版的章节编号。"),
        ("附录一：规则", 1, "只保存经过授权的数据。"),
    ]:
        variant.add_heading(title, level)
        variant.add_paragraph(body)
    variant.save(root / "chapter-before.docx")

    deck = Presentation()
    deck.slide_width, deck.slide_height = Inches(10), Inches(5.625)

    def label(slide, text, x, y, w=3, h=.6):
        box = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
        box.text = text
        for paragraph in box.text_frame.paragraphs:
            paragraph.font.size = Pt(24)
        return box

    slide = deck.slides.add_slide(deck.slide_layouts[6])
    label(slide, "Review flow", .4, .3, 8)
    label(slide, "ALPHA", 1, 2)
    label(slide, "BETA", 6.5, 2)
    # Only geometry encodes BETA -> ALPHA. Text order alone suggests the reverse.
    slide.shapes.add_shape(MSO_SHAPE.LEFT_ARROW, Inches(3.8), Inches(2), Inches(2), Inches(.7))
    group = slide.shapes.add_group_shape()
    group.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(1), Inches(4.2), Inches(2), Inches(.3))
    label(slide, "decorative footer", 1, 4.6, 5)

    slide = deck.slides.add_slide(deck.slide_layouts[6])
    label(slide, "Observed throughput (units/hour)", .3, .2, 9)
    img = Image.new("RGB", (1200, 500), "white")
    draw = ImageDraw.Draw(img)
    font_path = Path("/System/Library/Fonts/Supplemental/Arial.ttf")
    font = ImageFont.truetype(str(font_path) if font_path.exists() else "DejaVuSans.ttf", 48)
    for y, name, value, color in [(80, "KAPPA", 37, "#2079ba"), (280, "LAMBDA", 82, "#c35c28")]:
        draw.text((20, y), name, fill="black", font=font)
        draw.rectangle((270, y, 270 + value * 8, y + 75), fill=color)
        draw.text((290 + value * 8, y), str(value), fill="black", font=font)
    stream = io.BytesIO()
    img.save(stream, format="PNG")
    stream.seek(0)
    slide.shapes.add_picture(stream, Inches(.2), Inches(1.2), width=Inches(9.6))

    slide = deck.slides.add_slide(deck.slide_layouts[6])
    label(slide, "Different period: do not mix pages", .3, .2, 9)
    label(slide, "KAPPA 999 / LAMBDA 111", 1, 2, 8)
    deck.save(root / "visual-relations.pptx")
    # Held-out direction variant: identical label order, opposite arrow geometry.
    reverse = Presentation()
    reverse.slide_width, reverse.slide_height = Inches(10), Inches(5.625)
    slide = reverse.slides.add_slide(reverse.slide_layouts[6])
    label(slide, "Independent flow variant", .4, .3, 8)
    label(slide, "DELTA", 1, 2)
    label(slide, "GAMMA", 6.5, 2)
    slide.shapes.add_shape(MSO_SHAPE.RIGHT_ARROW, Inches(3.8), Inches(2), Inches(2), Inches(.7))
    reverse.save(root / "visual-reversed.pptx")
    (root / "location-visual-ground-truth.json").write_text(json.dumps({
        "chapter-order.docx": {"old_heading": "document.p[3]", "appendix_a": "document.p[5]", "appendix_b": "document.p[7]", "final_parent": "document.p[9]", "final_heading": "document.p[11]", "final_items": [12, 13, 14], "relation": "final checklist AFTER both appendices, outside numbered section 21"},
        "chapter-before.docx": {"heading": "document.p[3]", "parent": "document.p[1]", "appendices": [5, 7], "relation": "checklist BEFORE both appendices; parent has no chapter number"},
        "visual-relations.pptx": {"1": {"arrow": "BETA -> ALPHA", "decorative_group": True}, "2": {"KAPPA": 37, "LAMBDA": 82, "difference": 45, "unit": "units/hour", "image_only": True}, "3": {"out_of_scope_decoy": [999, 111]}},
        "visual-reversed.pptx": {"1": {"arrow": "DELTA -> GAMMA"}}
    }, ensure_ascii=False, indent=2), encoding="utf-8")


if __name__ == "__main__":
    main()
