
import json, sys, os
try:
    from docx import Document
except Exception:
    print(json.dumps({"error":"DOCX adapter requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
replacements = req.get("replacements") or []
counts = {item["Find"] if "Find" in item else item.get("find"): 0 for item in replacements}

def find_value(item):
    return item["Find"] if "Find" in item else item.get("find", "")

def replace_value(item):
    return item["Replace"] if "Replace" in item else item.get("replace", "")

def replace_in_paragraph(paragraph):
    total = 0
    for item in replacements:
        find = find_value(item)
        repl = replace_value(item)
        if not find:
            continue
        full = "".join(run.text for run in paragraph.runs)
        count = full.count(find)
        if count <= 0:
            continue
        next_text = full.replace(find, repl)
        for run in paragraph.runs:
            run.text = ""
        if paragraph.runs:
            paragraph.runs[0].text = next_text
        else:
            paragraph.add_run(next_text)
        counts[find] = counts.get(find, 0) + count
        total += count
    return total

try:
    doc = Document(req["path"])
    total = 0
    for paragraph in doc.paragraphs:
        total += replace_in_paragraph(paragraph)
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                for paragraph in cell.paragraphs:
                    total += replace_in_paragraph(paragraph)
    missing = [find for find, count in counts.items() if count == 0]
    if missing:
        print(json.dumps({"error":"find text was not matched: " + ", ".join(repr(x) for x in missing)}))
        sys.exit(0)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    doc.save(req["output_path"])
    print(json.dumps({"replacements":total,"bytes":os.path.getsize(req["output_path"]),"details":[{"find":k,"count":v} for k,v in counts.items()]}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
