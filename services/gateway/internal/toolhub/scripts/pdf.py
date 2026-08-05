
import base64, io, json, sys, os, unicodedata
try:
    from pypdf import PdfReader, PdfWriter
except Exception:
    print(json.dumps({"error":"PDF adapter requires pypdf"}))
    sys.exit(0)
try:
    import pypdfium2 as pdfium
    from PIL import Image
except Exception:
    pdfium = None
    Image = None

req = json.load(sys.stdin)
op = req.get("operation")

MAX_OCR_PAGES = 8
MAX_OCR_PAGE_BYTES = 4 << 20
MAX_OCR_TOTAL_BYTES = 16 << 20
PDF_NATIVE_TEXT_QUALITY_VERSION = "pdf_native_text_quality_v1"
PDF_OCR_PREPROCESSING_VERSION = "pdf_page_render_v1"

def native_text_quality(text, image_count):
    stripped = text.strip()
    if not stripped:
        return {
            "classification":"empty", "reason_codes":["native_text_empty"],
            "version":PDF_NATIVE_TEXT_QUALITY_VERSION,
            "features":{"characters":0,"meaningful_characters":0,"image_count":image_count},
        }
    characters = [value for value in stripped if not value.isspace()]
    count = len(characters)
    meaningful = sum(1 for value in characters if unicodedata.category(value)[:1] in ("L", "N"))
    replacements = sum(1 for value in characters if value == "\ufffd")
    controls = sum(1 for value in characters if unicodedata.category(value) == "Cc")
    repeated = 1
    longest_repeat = 1
    for index in range(1, count):
        if characters[index] == characters[index - 1]:
            repeated += 1
            longest_repeat = max(longest_repeat, repeated)
        else:
            repeated = 1
    reasons = []
    if replacements * 50 > count:
        reasons.append("replacement_character_ratio")
    if controls * 50 > count:
        reasons.append("control_character_ratio")
    if meaningful < 3 or meaningful * 100 < count * 30:
        reasons.append("low_meaningful_character_ratio")
    if count >= 12 and longest_repeat * 100 >= count * 40:
        reasons.append("repeated_glyph_run")
    if image_count > 0 and meaningful < 48:
        reasons.append("sparse_text_with_page_image")
    return {
        "classification":"degraded" if reasons else "usable",
        "reason_codes":reasons,
        "version":PDF_NATIVE_TEXT_QUALITY_VERSION,
        "features":{
            "characters":count, "meaningful_characters":meaningful,
            "replacement_characters":replacements, "control_characters":controls,
            "longest_repeated_glyph_run":longest_repeat, "image_count":image_count,
        },
    }

def render_page_for_ocr(document, page_index):
    if document is None or Image is None:
        return None
    page = document[page_index]
    bitmap = page.render(scale=2.0)
    try:
        image = bitmap.to_pil().convert("RGB")
        if max(image.size) > 2400:
            image.thumbnail((2400, 2400), Image.Resampling.LANCZOS)
        for attempt in range(6):
            output = io.BytesIO()
            image.save(output, format="JPEG", quality=max(62, 92 - attempt * 6), optimize=True)
            data = output.getvalue()
            if len(data) <= MAX_OCR_PAGE_BYTES:
                return data
            image = image.resize((max(1, image.width * 4 // 5), max(1, image.height * 4 // 5)), Image.Resampling.LANCZOS)
        return None
    finally:
        bitmap.close()
        page.close()

def page_indexes(pages, count):
    if not pages:
        raise ValueError("pages are required")
    out = []
    for page in pages:
        idx = int(page) - 1
        if idx < 0 or idx >= count:
            raise ValueError("page out of range: %s" % page)
        out.append(idx)
    return out

try:
    if op in ("extract_text", "read"):
        reader = PdfReader(req["path"])
        pages = []
        chunks = []
        images = []
        resources = []
        annotations = []
        page_settings = []
        extracted_bytes = 0
        scanned_pages = 0
        rendered_ocr_pages = 0
        rendered_ocr_bytes = 0
        pdfium_document = pdfium.PdfDocument(req["path"]) if pdfium is not None else None
        for index, page in enumerate(reader.pages, start=1):
            text = page.extract_text() or ""
            extracted_bytes += len(text.encode("utf-8")) + (2 if chunks else 0)
            rotation = int(page.get("/Rotate", 0) or 0)
            media_box = [float(value) for value in page.mediabox]
            crop_box = [float(value) for value in page.cropbox]
            page_settings.append({"index": index, "rotation": rotation, "media_box": media_box, "crop_box": crop_box, "path": "document.page[%d]" % index})
            try:
                page_images = list(page.images)
            except Exception:
                page_images = []
            quality = native_text_quality(text, len(page_images))
            needs_ocr = quality["classification"] != "usable"
            text_status = "native"
            text_source = "native"
            text_status_reason = "native_text_usable"
            ocr_resource_key = None
            if needs_ocr:
                scanned_pages += 1
                text_status = "budget_omitted"
                text_status_reason = "ocr_page_budget_exhausted"
                if rendered_ocr_pages < MAX_OCR_PAGES and rendered_ocr_bytes < MAX_OCR_TOTAL_BYTES:
                    try:
                        data = render_page_for_ocr(pdfium_document, index - 1)
                    except Exception:
                        data = None
                    if data is None:
                        text_status = "render_failed"
                        text_status_reason = "ocr_page_render_failed"
                    elif rendered_ocr_bytes + len(data) <= MAX_OCR_TOTAL_BYTES:
                        resource_key = "pdf:page:%d:ocr" % index
                        ocr_resource_key = resource_key
                        images.append({
                            "kind": "page_image", "resource_key": resource_key, "parent_path": "document.page[%d]" % index,
                            "location": {"page_index": index, "path": "document.page[%d]" % index},
                            "source": {"parser": "pypdfium2", "part_name": "page-%d.jpg" % index}, "content_type": "image/jpeg",
                        })
                        resources.append({"key": resource_key, "kind": "page_image", "content_type": "image/jpeg", "data_base64": base64.b64encode(data).decode("ascii")})
                        rendered_ocr_pages += 1
                        rendered_ocr_bytes += len(data)
                        text_status = "ocr_pending"
                        text_status_reason = "ocr_requested"
                if not text.strip():
                    text_source = "none"
            page_record = {
                "index": index, "text": text.strip(), "text_source": text_source, "text_status": text_status,
                "text_status_reason": text_status_reason, "native_text_quality": quality,
                "rotation": rotation, "media_box": media_box, "crop_box": crop_box,
            }
            if needs_ocr:
                page_record["ocr_preprocessing_version"] = PDF_OCR_PREPROCESSING_VERSION
            if ocr_resource_key is not None:
                page_record["ocr_provenance_ref"] = ocr_resource_key
            pages.append(page_record)
            try:
                for image_index, image in enumerate(page_images, start=1):
                    data = bytes(image.data)
                    content_type = str(getattr(image, "content_type", "") or "")
                    if not content_type:
                        extension = os.path.splitext(str(image.name or ""))[1].lower()
                        content_type = {".png":"image/png", ".jpg":"image/jpeg", ".jpeg":"image/jpeg", ".gif":"image/gif", ".webp":"image/webp"}.get(extension, "application/octet-stream")
                    resource_key = "pdf:page:%d:image:%d:%s" % (index, image_index, str(image.name or ""))
                    images.append({
                        "kind": "image", "resource_key": resource_key, "parent_path": "document.page[%d]" % index,
                        "location": {"page_index": index, "image_index": image_index, "path": "document.page[%d].image[%d]" % (index, image_index)},
                        "source": {"parser": "pypdf", "part_name": str(image.name or "")}, "content_type": content_type,
                    })
                    resources.append({"key": resource_key, "kind": "image", "content_type": content_type, "data_base64": base64.b64encode(data).decode("ascii")})
            except Exception:
                pass
            try:
                for annotation_index, reference in enumerate(page.get("/Annots") or [], start=1):
                    annotation = reference.get_object()
                    action = annotation.get("/A") or {}
                    annotations.append({
                        "kind": str(annotation.get("/Subtype") or "annotation").lstrip("/"),
                        "text": str(annotation.get("/Contents") or ""),
                        "target": str(action.get("/URI") or ""),
                        "rect": [float(value) for value in (annotation.get("/Rect") or [])],
                        "location": {"page_index": index, "annotation_index": annotation_index, "path": "document.page[%d].annotation[%d]" % (index, annotation_index)},
                        "source": {"parser": "pypdf"},
                    })
            except Exception:
                pass
            chunks.append(text)
        if pdfium_document is not None:
            pdfium_document.close()
        text = "\n\n".join(chunks).strip()
        max_bytes = int(req.get("max_bytes") or 20000)
        raw = text.encode("utf-8")
        truncated = len(raw) > max_bytes
        if truncated:
            text = raw[:max_bytes].decode("utf-8", errors="ignore")
        status_counts = {}
        missing_page_indexes = []
        for page in pages:
            status = page["text_status"]
            status_counts[status] = status_counts.get(status, 0) + 1
            if status != "native":
                missing_page_indexes.append(page["index"])
        read_complete = not truncated and not missing_page_indexes
        out = {"content":text,"truncated":truncated,"extracted_bytes":extracted_bytes,"scanned_unsupported":bool(missing_page_indexes),"resources":resources}
        if op == "read":
            out["document"] = {
                "schema_version":"document_read_v1","format":"pdf","source":"pypdf","pages":pages,
                "enrichment": {
                    "schema_version":"document_enrichment_v1",
                    "assets":{"images":images,"charts":[],"embedded_objects":[]},
                    "annotations":{"comments":annotations,"notes":[],"hyperlinks":[]},
                    "layout":{"sections":[],"page_settings":page_settings,"slide_layouts":[],"merged_ranges":[]},
                    "extensions":{"status":"deferred","parts":[]},
                    "coverage":{"content":"complete" if read_complete else "partial","assets":"partial","annotations":"partial","layout":"partial","extensions":"deferred"},
                },
                "stats":{
                    "pages":len(pages),"images":len(images),"annotations":len(annotations),"scanned_pages":scanned_pages,
                    "ocr_page_images":rendered_ocr_pages,"ocr_pages_omitted":sum(1 for page in pages if page["text_status"] == "budget_omitted"),
                    "page_status_counts":status_counts,"missing_page_indexes":missing_page_indexes,
                    "read_complete":read_complete,"coverage_status":"complete" if read_complete else ("unavailable" if not text.strip() else "partial"),
                    "scanned_unsupported":bool(missing_page_indexes),
                }
            }
        print(json.dumps(out))
    elif op == "merge":
        writer = PdfWriter()
        for path in req["inputs"]:
            reader = PdfReader(path)
            for page in reader.pages:
                writer.add_page(page)
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages),"inputs":req["inputs"]}))
    elif op in ("extract_pages", "delete_pages", "rotate_pages"):
        reader = PdfReader(req["path"])
        writer = PdfWriter()
        selected = set(page_indexes(req.get("pages"), len(reader.pages)))
        if op == "rotate_pages":
            rotation = int(req.get("rotation") or 90)
            if rotation % 90 != 0:
                raise ValueError("rotation must be a multiple of 90")
        for i, page in enumerate(reader.pages):
            if op == "extract_pages" and i not in selected:
                continue
            if op == "delete_pages" and i in selected:
                continue
            if op == "rotate_pages" and i in selected:
                page.rotate(rotation)
            writer.add_page(page)
        if len(writer.pages) == 0:
            raise ValueError("operation would produce an empty PDF")
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages)}))
    elif op == "split":
        reader = PdfReader(req["path"])
        if len(reader.pages) == 0:
            raise ValueError("cannot split an empty PDF")
        base, ext = os.path.splitext(req["output_path"])
        outputs = []
        for i, page in enumerate(reader.pages, start=1):
            writer = PdfWriter()
            writer.add_page(page)
            if len(reader.pages) == 1:
                part_path = req["output_path"]
            else:
                part_path = "%s-page-%d%s" % (base, i, ext or ".pdf")
            os.makedirs(os.path.dirname(part_path), exist_ok=True)
            with open(part_path, "wb") as f:
                writer.write(f)
            outputs.append(part_path)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"outputs":outputs,"bytes":sum(os.path.getsize(p) for p in outputs),"pages":len(reader.pages)}))
    else:
        print(json.dumps({"error":"unsupported pdf operation: %s" % op}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
