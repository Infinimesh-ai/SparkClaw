#!/usr/bin/env python3
"""Check preserved originals and exact fixture edit scope, without a model.

DOCX checks package parts and normalized XML, including paragraph/run formatting.
They do not establish rendered pagination or native Word visual equivalence.
"""
import argparse
import hashlib
import json
from pathlib import Path
from xml.etree import ElementTree as ET
import zipfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_docx(source, output):
    with zipfile.ZipFile(source) as before, zipfile.ZipFile(output) as after:
        names = {n for n in before.namelist() if not n.endswith("/")}
        assert names == {n for n in after.namelist() if not n.endswith("/")}, "DOCX package parts changed"
        changed = []
        for name in sorted(names):
            a, b = before.read(name), after.read(name)
            if a == b:
                continue
            if name.endswith((".xml", ".rels")):
                left, right = ET.fromstring(a), ET.fromstring(b)
                if name == "word/document.xml":
                    ns = {"w": "http://schemas.openxmlformats.org/wordprocessingml/2006/main"}
                    paragraphs = left.findall(".//w:body/w:p", ns)
                    assert len(paragraphs) == 58
                    texts = paragraphs[42].findall(".//w:t", ns)
                    assert len(texts) == 1 and texts[0].text.strip() == "采访稿"
                    texts[0].text = "访谈模拟示例（尚未开展真实采访）"
                    texts[0].attrib.pop("{http://www.w3.org/XML/1998/namespace}space", None)
                    changed.append("document.p[43].text")
                assert ET.tostring(left) == ET.tostring(right), "Unexpected DOCX XML change: " + name
            else:
                raise AssertionError("Unexpected binary part change: " + name)
        assert changed == ["document.p[43].text"]
        return {"verified_change": changed, "other_package_parts_and_formatting_preserved": True}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", type=Path, required=True)
    parser.add_argument("--owner-files", type=Path, required=True)
    parser.add_argument("--runs", type=Path, nargs="+", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    originals = []
    for source, target in [("形策论文及访谈附件.docx", "paper.docx"),
                           ("爬虫学习路线_概念理解与观察实操手册.docx", "crawler.docx"),
                           ("计算机网络导论.pptx", "networks.pptx")]:
        original, copied = args.owner_files / source, args.workspace / target
        assert digest(original) == digest(copied), "Original/copy hash mismatch: " + target
        originals.append({"file": target, "sha256": digest(original), "unchanged": True})
    checks = []
    for directory in args.runs:
        for case in ("paper_edit", "markdown_edit"):
            record = directory / (case + ".json")
            if not record.exists():
                continue
            data = json.loads(record.read_text())
            attachments = data["result"]["message"]["attachments"]
            assert len(attachments) == 1
            relative = Path(attachments[0]["rel_path"])
            assert not relative.is_absolute() and ".." not in relative.parts
            output = args.workspace / relative
            if case == "paper_edit":
                detail = verify_docx(args.workspace / "paper.docx", output)
            else:
                original = (args.workspace / "retention.md").read_text()
                assert output.read_text() == original.replace("保留90天", "保留60天", 1)
                detail = {"verified_change": "ordinary retention 90 -> 60 days", "14_day_exception_and_other_bytes_preserved": True}
            checks.append({"case": case, "run_id": data["result"]["run"]["id"], "output": str(output), **detail})
    assert checks, "No maintenance attempts found"
    with args.output.open("x") as handle:
        json.dump({"originals": originals, "maintenance_checks": checks, "native_visual_verification": "not_performed"}, handle, ensure_ascii=False, indent=2)
    print(f"Verified {len(originals)} originals and {len(checks)} maintained copies")


if __name__ == "__main__":
    main()
