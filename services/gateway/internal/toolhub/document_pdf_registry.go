package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func pdfToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"pdf.extract_text": documentReadRegistration(
			ctxArgsSessionRun((*ToolHub).pdfExtractText),
			[]string{app.DocumentFormatPDF},
			"Extract bounded text and stable page evidence from a workspace PDF, using configured OCR for scanned pages.",
		),
		"pdf.transform": pdfTransformRegistration(),
	}
}

func pdfTransformRegistration() toolRegistration {
	registration := documentEditRegistration(ctxArgs((*ToolHub).pdfTransform), app.DocumentFormatPDF, "extract_pages", "Apply a bounded PDF transform and write an output copy.")
	registration.capabilities = registration.capabilities[:0]
	for _, operation := range []string{"extract_pages", "delete_pages", "rotate_pages", "split"} {
		registration.capabilities = append(registration.capabilities, app.CapabilityDescriptor{
			Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: app.DocumentFormatPDF, app.CapabilityQualifierOperation: operation},
		})
	}
	return registration
}
