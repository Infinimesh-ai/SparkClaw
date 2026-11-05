#!/usr/bin/env python3
"""Generate deterministic document-workflow fixtures using project dependencies.

The destination must be a new directory. Owner files are copied, never edited.
Answers are deliberately placed at the beginning, middle and end of sources.
"""
import argparse
import json
from pathlib import Path
import shutil
import subprocess

from PIL import Image, ImageDraw, ImageFont
from pypdf import PdfWriter
from pypdf.generic import DictionaryObject, NameObject, DecodedStreamObject


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("destination", type=Path)
    parser.add_argument("--owner-files", type=Path)
    args = parser.parse_args()
    args.destination.mkdir(parents=True, exist_ok=False)
    root = args.destination.resolve()
    if args.owner_files:
        for source, target in [
            ("形策论文及访谈附件.docx", "paper.docx"),
            ("爬虫学习路线_概念理解与观察实操手册.docx", "crawler.docx"),
            ("计算机网络导论.pptx", "networks.pptx"),
        ]:
            shutil.copy2(args.owner_files / source, root / target)

    (root / "retention.md").write_text(
        "# 数据留存规范\n\n一般业务记录保留90天。\n\n"
        "## 例外\n\n未成年人试点组记录仅保留14天，以此例外覆盖一般规则。\n\n"
        "## 审核\n\n每周五由资料管理员检查删除结果；规则自2026年10月1日生效。\n",
        encoding="utf-8",
    )
    lines = ["# 项目风险台账\n\n必须核对全表，记录编号与金额无关。金额单位：元。\n"]
    for i in range(1, 121):
        lines.append(f"## 记录 {i:03d}\n")
        if i in (1, 60, 120):
            lines.append(f"风险 R{i:03d}：状态未关闭。负责人为{'甲' if i == 1 else '乙' if i == 60 else '丙'}。必须在验收前处理。\n")
        else:
            lines.append(f"记录 C{i:03d}：常规检查已经完成，状态已关闭，无待办风险。" + "本条仅描述已完成的数据校验和归档工作，不增加新的整改要求。" * 6 + "\n")
    (root / "risk-register.md").write_text("\n".join(lines), encoding="utf-8")
    # A second inventory changes matching positions/identities and uses a
    # differently worded request; it is evaluated separately from regression cases.
    variant = ["# 整改跟踪表\n\n仅按每条记录正文状态判断，章节编号不是风险编号。\n"]
    for i in range(1, 98):
        variant.append(f"## 核对项 {i:03d}\n")
        active = {17: ("XR-09", "丁"), 53: ("Q-301", "戊"), 97: ("Z-888", "己")}.get(i)
        if active:
            variant.append(f"编号 {active[0]}；状态：未关闭；负责人：{active[1]}。请完成复核。\n")
        else:
            variant.append(f"编号 DONE-{i:03d}；状态：已关闭；验收与归档已完成，无剩余整改事项。" + "这是一条已关闭记录，不需要列入未关闭清单。" * 4 + "\n")
    (root / "risk-variant.md").write_text("\n".join(variant), encoding="utf-8")
    (root / "plain.txt").write_text("A组方案：预算120万元，工期8周。\nB组方案：预算95万元，工期11周。\n两方案均不含税；B组的95万元不含额外5万元迁移费。\n", encoding="utf-8")

    writer = PdfWriter()
    font = writer._add_object(DictionaryObject({NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"), NameObject("/BaseFont"): NameObject("/Helvetica")}))
    texts = ["STANDARD: Retain all routine records for 90 days.", "AUDIT: Review deletions every Friday.", "EXCEPTION: Pilot group ZETA records must be deleted after 14 days."]
    for text in texts:
        page = writer.add_blank_page(612, 792)
        page[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"): DictionaryObject({NameObject("/F1"): font})})
        stream = DecodedStreamObject()
        stream.set_data(f"BT /F1 12 Tf 40 720 Td ({text}) Tj ET".encode())
        page[NameObject("/Contents")] = writer._add_object(stream)
    with (root / "policy.pdf").open("wb") as handle:
        writer.write(handle)
    image = Image.new("RGB", (1400, 700), "white")
    draw = ImageDraw.Draw(image)
    font_path = Path("/System/Library/Fonts/Supplemental/Arial.ttf")
    font = ImageFont.truetype(str(font_path) if font_path.exists() else "DejaVuSans.ttf", 48)
    draw.text((60, 100), "SCANNED INVOICE\nTOTAL: 4286 USD\nREFERENCE: ZETA-17", font=font, fill="black", spacing=35)
    image.save(root / "scan.pdf", "PDF", resolution=150)

    # ExcelJS is the project's supported workbook dependency. Include a styled
    # empty tail, duplicate descriptions, units and an unrecalculated formula.
    script = r'''
const Excel = require('exceljs');
(async () => {
 const wb = new Excel.Workbook();
 const sheet = wb.addWorksheet('Budget');
 sheet.addRow(['单位：万元；不含税；金额合计必须排除已取消项目。']);
 sheet.addRow(['项目','金额','状态']);
 sheet.addRow(['Alpha',120,'有效']);
 sheet.addRow(['Beta',95,'有效']);
 sheet.addRow(['Migration',5,'有效']);
 sheet.addRow(['Cancelled',500,'已取消']);
 sheet.addRow(['缓存合计',{formula:'SUM(B3:B5)',result:999},'缓存未重算，不可用作当前合计']);
 sheet.getCell('Z100').font = {bold:true};
 await wb.xlsx.writeFile(process.argv[1]);
})();
'''
    repo = Path(__file__).resolve().parents[2]
    subprocess.run(["node", "-e", script, str(root / "budget.xlsx")], cwd=repo, check=True)
    print(json.dumps({"workspace": str(root), "files": sorted(p.name for p in root.iterdir())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
