
import base64, io, json, sys, os
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
            if extracted_bytes > int(req.get("max_bytes") or 20000):
                print(json.dumps({"content":"", "truncated":True, "extracted_bytes":extracted_bytes, "scanned_unsupported":False}))
                sys.exit(0)
            rotation = int(page.get("/Rotate", 0) or 0)
            media_box = [float(value) for value in page.mediabox]
            crop_box = [float(value) for value in page.cropbox]
            pages.append({"index": index, "text": text.strip(), "rotation": rotation, "media_box": media_box, "crop_box": crop_box})
            page_settings.append({"index": index, "rotation": rotation, "media_box": media_box, "crop_box": crop_box, "path": "document.page[%d]" % index})
            if not text.strip():
                scanned_pages += 1
                if rendered_ocr_pages < MAX_OCR_PAGES:
                    data = render_page_for_ocr(pdfium_document, index - 1)
                    if data is not None and rendered_ocr_bytes + len(data) <= MAX_OCR_TOTAL_BYTES:
                        resource_key = "pdf:page:%d:ocr" % index
                        images.append({
                            "kind": "page_image", "resource_key": resource_key, "parent_path": "document.page[%d]" % index,
                            "location": {"page_index": index, "path": "document.page[%d]" % index},
                            "source": {"parser": "pypdfium2", "part_name": "page-%d.jpg" % index}, "content_type": "image/jpeg",
                        })
                        resources.append({"key": resource_key, "kind": "page_image", "content_type": "image/jpeg", "data_base64": base64.b64encode(data).decode("ascii")})
                        rendered_ocr_pages += 1
                        rendered_ocr_bytes += len(data)
            try:
                for image_index, image in enumerate(page.images, start=1):
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
        if not text:
            out = {"content":"","truncated":False,"extracted_bytes":0,"scanned_unsupported":True,"resources":resources}
            if op == "read":
                out["document"] = {
                    "schema_version":"document_read_v1","format":"pdf","source":"pypdf","pages":pages,
                    "enrichment": {
                        "schema_version":"document_enrichment_v1",
                        "assets":{"images":images,"charts":[],"embedded_objects":[]},
                        "annotations":{"comments":annotations,"notes":[],"hyperlinks":[]},
                        "layout":{"sections":[],"page_settings":page_settings,"slide_layouts":[],"merged_ranges":[]},
                        "extensions":{"status":"deferred","parts":[]},
                        "coverage":{"content":"partial","assets":"partial","annotations":"partial","layout":"partial","extensions":"deferred"},
                    },
                    "stats":{"pages":len(pages),"images":len(images),"annotations":len(annotations),"scanned_pages":scanned_pages,"ocr_page_images":rendered_ocr_pages,"ocr_pages_omitted":max(0,scanned_pages-rendered_ocr_pages),"scanned_unsupported":True}
                }
            print(json.dumps(out))
            sys.exit(0)
        max_bytes = int(req.get("max_bytes") or 20000)
        raw = text.encode("utf-8")
        truncated = len(raw) > max_bytes
        if truncated:
            text = raw[:max_bytes].decode("utf-8", errors="ignore")
        out = {"content":text,"truncated":truncated,"extracted_bytes":extracted_bytes,"scanned_unsupported":scanned_pages > 0,"resources":resources}
        if op == "read":
            out["document"] = {
                "schema_version":"document_read_v1","format":"pdf","source":"pypdf","pages":pages,
                "enrichment": {
                    "schema_version":"document_enrichment_v1",
                    "assets":{"images":images,"charts":[],"embedded_objects":[]},
                    "annotations":{"comments":annotations,"notes":[],"hyperlinks":[]},
                    "layout":{"sections":[],"page_settings":page_settings,"slide_layouts":[],"merged_ranges":[]},
                    "extensions":{"status":"deferred","parts":[]},
                    "coverage":{"content":"partial" if scanned_pages else "complete","assets":"partial","annotations":"partial","layout":"partial","extensions":"deferred"},
                },
                "stats":{"pages":len(pages),"images":len(images),"annotations":len(annotations),"scanned_pages":scanned_pages,"ocr_page_images":rendered_ocr_pages,"ocr_pages_omitted":max(0,scanned_pages-rendered_ocr_pages),"scanned_unsupported":scanned_pages > 0}
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
