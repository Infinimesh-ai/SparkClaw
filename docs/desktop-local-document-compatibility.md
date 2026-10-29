# Local document compatibility qualification

> Language: English | [简体中文](../zh-cn/docs/desktop-local-document-compatibility.md)

This is a finite, reproducible release matrix for the read-only local viewer. It qualifies the supported cases and controlled rejection behavior below; it does not promise Microsoft Office pagination, every font, animation, password entry, or unrestricted embedded content. The [browser design](desktop-local-file-browser-design.md) describes the unchanged isolation and resource limits.

## Qualified matrix

| Area | Synthetic input and required result |
|---|---|
| DOCX and genuine legacy DOC | Two pages, portrait followed by landscape; Chinese and English, header/footer, numbered paragraphs, merged table cells and an image; sandbox conversion, correct page geometry, text/position checks and visible rendering |
| PPTX and genuine legacy PPT | Three wide slides containing Chinese/English text, a colored image and table; all three pages and expected table values remain visible |
| Missing OOXML font | A deliberately absent family produces engine-reported feedback, a usable fallback PDF and a local dismissible notice; closing the notice preserves reading |
| Encrypted DOCX/PPTX | Genuine encrypted packages, independently decrypted back to the original bytes by the fixture generator, are refused; no password prompt or plaintext preview is attempted |
| Encrypted PDF | AES-256 fixture plus an independently checked Standard Security R2 fixture; refuse visibly with an instruction to unlock and save a separate copy, without invoking a native password dialog |
| Corrupt/wrong-signature Office and PDF | Truncated ZIP, invalid OLE structures, text masquerading as DOC/PPT and malformed PDF fail; subsequent ordinary documents remain usable |
| Embedded Office content | OOXML containers with embedded-object entries are refused before conversion |
| Images | PNG, JPEG, WebP, static GIF and safe SVG render; corrupt PNG, excessive dimensions, animated GIF and active/external-resource SVG fail visibly |
| Unsupported raster formats | BMP is outside the qualified signature decoder and is refused; AVIF is likewise not claimed as supported |
| Text | UTF-8 Chinese/English renders literally; a multibyte character straddling the 256 KiB read window survives Next; binary/NUL and invalid UTF-8 fail visibly |
| Markdown | Script markup is inert; remote images and active hyperlinks are not admitted, and unavailable relative images do not escape the viewer |
| CSV/XLSX | Literal strings and formula-looking CSV values; malformed quoting/UTF-8 refusal; XLSX cached zero/false, absent cached results, merges, two sheets and read-only selection/switching |

The [2026-10-29 machine-readable receipt](evidence/desktop-local-format-matrix-2026-10-29.json) records 19 broker cases and 25 native viewer cases on Mac. Five converted Office documents, totaling 11 pages, also pass independent PDF page geometry, expected text, coordinate-ordered Chinese glyph and nonblank raster checks. Visual review of the PDFium contact sheet confirms the expected tables, images, mixed orientation and slide layouts; the native viewer checks each document's canvas and the dismissible font notice. This is not a pixel comparison against Microsoft Office. Every broker source hash is unchanged. These are source/isolated-run results; package and installed-profile results are recorded by the separate release acceptance.

The generator creates 37 original files. The broker covers 18 of those files plus one generated XLSX (19 cases); the native viewer covers the other 19 original files, five PDFs derived from the accepted Office originals, and the same XLSX (25 cases). Together these are 38 distinct source documents and 44 stage executions. The receipt maps every filename to its stage. The existing native regression also authors a separate encrypted R2 PDF, outside the 37-file generator count.

## Fidelity and support limits

- Static previews do not edit or recalculate documents and do not execute macros, animations or embedded objects. Password-protected files must be unlocked in another application and saved as separate unencrypted copies.
- The user explicitly exempted encrypted legacy binary DOC/PPT verification on 2026-10-29. This is outside the required release scope and is not a release blocker; it is not recorded as a pass. Protected OOXML and PDF use genuine encrypted samples. The matrix is a selected set of reproducible cases, not every format/condition combination.
- The engine reports declared missing font families for OOXML. Its legacy binary DOC/PPT result does not provide a complete missing-font inventory; an empty warning list is not proof of exact font matching. No font is fetched remotely.
- Source fonts and the installed system font set can change pagination or appearance. The missing-font notice is shown only for a document with engine feedback, can be dismissed, and leaves the reader layout and controls intact.
- In the tested DOC/DOCX export, Chinese glyphs are spatially correct while the PDF logical extraction order can group separate font runs differently. Copy/extraction order is therefore not guaranteed to reproduce the original sentence. The matrix records this discrepancy rather than treating visual success as exact semantic extraction.
- Some unsupported images/text currently show decoder-level errors. This qualifies visible bounded rejection, not a claim that every error message is localized or that those formats are supported.

Before that exemption, the available authoring build (LibreOfficeDev 26.8.0.0.alpha0, revision `2c87e51eeaa2b413ff4ae097b2705eea1995d8e5`) was probed with disposable profiles. CLI `Password` filter options produced genuine but unencrypted DOC/PPT according to independent `msoffcrypto` inspection. The Basic UNO `storeAsURL` probe returned without its initial execution marker or an output file, and Python UNO bindings were absent. These observations qualify this environment's attempted authoring paths only; they do not establish a general LibreOffice encryption limitation. Further probing stopped when the user waived the requirement.

## Reproduce

The authoring toolchain is separate from the product runtime. Install the fixture requirements into a disposable Python environment and make a LibreOffice CLI available as `soffice`, or pass `--soffice`. It is used solely to export genuine DOC/PPT fixtures; the product always uses its bundled engine.

```sh
python3 -m pip install -r apps/desktop/test/document-format-fixtures.requirements.txt
python3 apps/desktop/test/generate-document-format-fixtures.py /tmp/sparkx-format-fixtures
node apps/desktop/test/run-local-preview-format-matrix.mjs /tmp/sparkx-format-fixtures /tmp/sparkx-format-results
python3 apps/desktop/test/inspect-document-format-pdfs.py /tmp/sparkx-format-results
npm run build:desktop-ui
node apps/desktop/test/run-local-preview-format-native.mjs /tmp/sparkx-format-fixtures /tmp/sparkx-format-results
node apps/desktop/test/run-local-preview-native.mjs
```

The generator refuses an existing fixture directory. Preserve its manifest and result JSON with each run: encrypted packages use fresh salts, so regenerated ciphertext hashes need not match a prior run. `SPARKCLAW_FORMAT_MODULES` and `SPARKCLAW_PREVIEW_RUNTIME` select a candidate's exact broker modules and worker executable; `SPARKCLAW_PREVIEW_TEST_ASSETS` selects exact viewer assets. `SPARKCLAW_FORMAT_SCREENSHOTS` optionally retains native screenshots. On Linux, run the native commands under the qualified display setup; Mac observations cannot substitute for Linux engine/isolation evidence.
