package toolhub

import "embed"

// Adapter scripts are maintained as standalone files under scripts/ so they
// can be linted and edited as real Python/JavaScript sources.

//go:embed scripts/docx_read.py
var docxReadAdapterScript string

//go:embed scripts/docx_structure.py
var docxStructureAdapterScript string

//go:embed scripts/pptx_read.py
var pptxReadAdapterScript string

//go:embed scripts/xlsx_read.js
var xlsxReadAdapterScript string

//go:embed scripts/docx_edit.py
var docxAdapterScript string

//go:embed scripts/pptx_edit.py
var pptxAdapterScript string

//go:embed scripts/pptx_visual_qa.py
var pptxVisualQAAdapterScript string

//go:embed scripts/pptx_visual_repair.py
var pptxVisualRepairAdapterScript string

//go:embed scripts/pptx_slide/*.py
var pptxSlideAdapterPackage embed.FS

//go:embed scripts/xlsx_edit.js
var xlsxAdapterScript string

//go:embed scripts/xlsx_structure.js
var xlsxStructureAdapterScript string

//go:embed scripts/pdf.py
var pdfAdapterScript string

//go:embed scripts/browser_readability.js
var browserReadabilityAdapterScript string
