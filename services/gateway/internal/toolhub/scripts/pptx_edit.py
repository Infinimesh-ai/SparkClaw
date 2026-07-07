
import json, sys, os
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX adapter requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
replacements = req.get("replacements") or []
counts = {item["Find"] if "Find" in item else item.get("find"): 0 for item in replacements}

def find_value(item):
    return item["Find"] if "Find" in item else item.get("find", "")

def replace_value(item):
    return item["Replace"] if "Replace" in item else item.get("replace", "")

def replace_in_text_frame(tf):
    total = 0
    for paragraph in tf.paragraphs:
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
                paragraph.add_run().text = next_text
            counts[find] = counts.get(find, 0) + count
            total += count
    return total

try:
    prs = Presentation(req["path"])
    total = 0
    for slide in prs.slides:
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape.has_text_frame:
                total += replace_in_text_frame(shape.text_frame)
            if hasattr(shape, "table"):
                for row in shape.table.rows:
                    for cell in row.cells:
                        total += replace_in_text_frame(cell.text_frame)
    missing = [find for find, count in counts.items() if count == 0]
    if missing:
        print(json.dumps({"error":"find text was not matched: " + ", ".join(repr(x) for x in missing)}))
        sys.exit(0)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    prs.save(req["output_path"])
    print(json.dumps({"replacements":total,"bytes":os.path.getsize(req["output_path"]),"details":[{"find":k,"count":v} for k,v in counts.items()]}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
