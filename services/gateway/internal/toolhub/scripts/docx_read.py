import base64, json, sys
try:
    import docx
    from docx.oxml.ns import qn
except Exception:
    print(json.dumps({"error":"DOCX reader requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
max_bytes = int(req.get("max_bytes") or 20000)

def trim(text):
    return " ".join(str(text or "").split())

def enum_value(value):
    if value is None:
        return ""
    return str(value)

def length_value(value):
    return int(value) if value is not None else 0

extracted_bytes = 0

def append_line(lines, text):
    global extracted_bytes
    extracted_bytes += len(text.encode("utf-8")) + (1 if lines else 0)
    if extracted_bytes > max_bytes:
        print(json.dumps({"content":"", "truncated":True, "extracted_bytes":extracted_bytes}))
        sys.exit(0)
    lines.append(text)

def paragraph_fields(paragraph):
    style = paragraph.style.name if paragraph.style is not None else ""
    outline_level = ""
    try:
        outline_level = enum_value(paragraph.paragraph_format.outline_level)
    except Exception:
        pass
    num_id = ""
    try:
        num_pr = paragraph._p.pPr.numPr if paragraph._p.pPr is not None else None
        if num_pr is not None and num_pr.numId is not None:
            num_id = str(num_pr.numId.val)
    except Exception:
        pass
    return style, outline_level, num_id

def paragraph_hyperlinks(paragraph, location, part, hyperlinks):
    for hyperlink in paragraph._p.xpath(".//w:hyperlink"):
        rel_id = hyperlink.get(qn("r:id")) or ""
        target = ""
        if rel_id and rel_id in part.rels:
            target = str(part.rels[rel_id].target_ref)
        text = trim("".join(hyperlink.itertext()))
        hyperlinks.append({
            "kind": "hyperlink",
            "text": text,
            "target": target,
            "location": dict(location),
            "source": {"parser": "python_docx", "relationship_id": rel_id},
        })

def paragraph_image_rel_ids(paragraph):
    return [value for value in paragraph._p.xpath(".//a:blip/@r:embed") if value]

try:
    document = docx.Document(req["path"])
    paragraphs = []
    blocks = []
    tables = []
    lines = []
    block_index = 0
    images = []
    resources = []
    resource_keys = set()
    registered_locations = set()
    hyperlinks = []

    def register_image(part, rel_id, parent_path, location):
        if not rel_id or rel_id not in part.related_parts:
            return
        related = part.related_parts[rel_id]
        content_type = str(getattr(related, "content_type", "application/octet-stream"))
        if not content_type.startswith("image/"):
            return
        part_name = str(getattr(related, "partname", ""))
        owner_part = str(getattr(part, "partname", ""))
        resource_key = "%s:%s:%s" % (owner_part, rel_id, part_name)
        location_key = "%s:%s" % (resource_key, location.get("path", ""))
        if location_key in registered_locations:
            return
        registered_locations.add(location_key)
        blob = bytes(related.blob)
        images.append({
            "kind": "image",
            "resource_key": resource_key,
            "parent_path": parent_path,
            "location": location,
            "source": {
                "parser": "python_docx",
                "relationship_id": rel_id,
                "part_name": part_name,
                "owner_part": owner_part,
            },
            "content_type": content_type,
        })
        if resource_key not in resource_keys:
            resource_keys.add(resource_key)
            resources.append({
                "key": resource_key,
                "kind": "image",
                "content_type": content_type,
                "data_base64": base64.b64encode(blob).decode("ascii"),
            })

    for index, paragraph in enumerate(document.paragraphs, start=1):
        path = "document.p[%d]" % index
        location = {
            "part": "document",
            "part_kind": "body",
            "block_type": "paragraph",
            "paragraph_index": index,
            "table_index": 0,
            "row_index": 0,
            "cell_index": 0,
            "cell_paragraph_index": 0,
            "path": path,
        }
        for image_index, rel_id in enumerate(paragraph_image_rel_ids(paragraph), start=1):
            register_image(document.part, rel_id, path, dict(location, block_type="image", image_index=image_index, path=path+".image[%d]" % image_index))
        paragraph_hyperlinks(paragraph, location, document.part, hyperlinks)
        text = trim(paragraph.text)
        if not text:
            continue
        block_index += 1
        location["block_index"] = block_index
        style, outline_level, num_id = paragraph_fields(paragraph)
        item = {
            "index": index,
            "text": text,
            "style": style,
            "outline_level": outline_level,
            "list_id": num_id,
            "part_kind": "body",
            "location": location,
        }
        paragraphs.append(item)
        blocks.append({"text": text, "style": style, "level": outline_level, "location": location})
        append_line(lines, text)

    for table_index, table in enumerate(document.tables, start=1):
        rows = []
        for row_index, row in enumerate(table.rows, start=1):
            row_values = []
            for cell_index, cell in enumerate(row.cells, start=1):
                cell_texts = []
                for cell_paragraph_index, paragraph in enumerate(cell.paragraphs, start=1):
                    path = "document.table[%d].row[%d].cell[%d].p[%d]" % (table_index, row_index, cell_index, cell_paragraph_index)
                    location = {
                        "part": "document",
                        "part_kind": "table_cell",
                        "block_type": "table_cell",
                        "paragraph_index": 0,
                        "table_index": table_index,
                        "row_index": row_index,
                        "cell_index": cell_index,
                        "cell_paragraph_index": cell_paragraph_index,
                        "path": path,
                    }
                    for image_index, rel_id in enumerate(paragraph_image_rel_ids(paragraph), start=1):
                        register_image(document.part, rel_id, path, dict(location, block_type="image", image_index=image_index, path=path+".image[%d]" % image_index))
                    paragraph_hyperlinks(paragraph, location, document.part, hyperlinks)
                    text = trim(paragraph.text)
                    if not text:
                        continue
                    cell_texts.append(text)
                    block_index += 1
                    location["block_index"] = block_index
                    style, outline_level, num_id = paragraph_fields(paragraph)
                    blocks.append({"text": text, "style": style, "level": outline_level, "location": location})
                    append_line(lines, text)
                row_values.append("\n".join(cell_texts))
            rows.append({"index": row_index, "cells": row_values})
        tables.append({"index": table_index, "rows": rows})

    section_layouts = []
    for section_index, section in enumerate(document.sections, start=1):
        section_path = "document.section[%d]" % section_index
        section_layouts.append({
            "index": section_index,
            "path": section_path,
            "orientation": enum_value(section.orientation),
            "start_type": enum_value(section.start_type),
            "page_width": length_value(section.page_width),
            "page_height": length_value(section.page_height),
            "top_margin": length_value(section.top_margin),
            "right_margin": length_value(section.right_margin),
            "bottom_margin": length_value(section.bottom_margin),
            "left_margin": length_value(section.left_margin),
            "header_linked_to_previous": bool(section.header.is_linked_to_previous),
            "footer_linked_to_previous": bool(section.footer.is_linked_to_previous),
        })
        for part_kind, container in (("header", section.header), ("footer", section.footer)):
            part = container.part
            for paragraph_index, paragraph in enumerate(container.paragraphs, start=1):
                parent_path = "%s.%s.p[%d]" % (section_path, part_kind, paragraph_index)
                for image_index, rel_id in enumerate(paragraph_image_rel_ids(paragraph), start=1):
                    register_image(part, rel_id, parent_path, {
                        "part": "document", "part_kind": part_kind, "block_type": "image",
                        "section_index": section_index, "paragraph_index": paragraph_index,
                        "image_index": image_index, "path": parent_path+".image[%d]" % image_index,
                    })

    for rel_id, relation in document.part.rels.items():
        if str(relation.reltype).endswith("/image"):
            related = document.part.related_parts.get(rel_id)
            resource_key = "%s:%s:%s" % (str(document.part.partname), rel_id, str(getattr(related, "partname", "")))
            if resource_key not in resource_keys:
                register_image(document.part, rel_id, "document", {
                    "part": "document", "part_kind": "body", "block_type": "image",
                    "path": "document.relationship[%s]" % rel_id,
                })

    comments = []
    try:
        for comment in document.comments:
            comments.append({
                "kind": "comment",
                "text": trim(comment.text),
                "author": str(comment.author or ""),
                "initials": str(comment.initials or ""),
                "location": {"path": "document.comment[%s]" % comment.comment_id},
                "source": {"parser": "python_docx", "comment_id": int(comment.comment_id)},
            })
    except Exception:
        pass

    content = "\n".join(lines).strip()
    raw = content.encode("utf-8")
    truncated = len(raw) > max_bytes
    if truncated:
        content = raw[:max_bytes].decode("utf-8", errors="ignore")
    print(json.dumps({
        "content": content,
        "truncated": truncated,
        "extracted_bytes": extracted_bytes,
        "resources": resources,
        "document": {
            "schema_version": "document_read_v1",
            "format": "docx",
            "source": "python_docx",
            "blocks": blocks,
            "paragraphs": paragraphs,
            "tables": tables,
            "enrichment": {
                "schema_version": "document_enrichment_v1",
                "assets": {"images": images, "charts": [], "embedded_objects": []},
                "annotations": {"comments": comments, "notes": [], "hyperlinks": hyperlinks},
                "layout": {"sections": section_layouts, "page_settings": section_layouts, "slide_layouts": [], "merged_ranges": []},
                "extensions": {"status": "deferred", "parts": []},
                "coverage": {"content": "complete", "assets": "complete", "annotations": "partial", "layout": "partial", "extensions": "deferred"},
            },
            "stats": {
                "blocks": len(blocks),
                "paragraphs": len(paragraphs),
                "tables": len(tables),
                "images": len(images),
                "comments": len(comments),
                "hyperlinks": len(hyperlinks),
                "complete": not truncated,
            }
        }
    }, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}))
