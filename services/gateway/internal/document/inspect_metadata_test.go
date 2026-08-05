package document

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestInferFormatFromMetadata(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{name: "notes.MD", want: app.DocumentFormatText},
		{name: "report.docx", contentType: "application/octet-stream", want: app.DocumentFormatDOCX},
		{name: "book", contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", want: app.DocumentFormatXLSX},
		{name: "deck", contentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation; charset=binary", want: app.DocumentFormatPPTX},
		{name: "scan", contentType: "application/pdf", want: app.DocumentFormatPDF},
		{name: "photo", contentType: "image/png", want: app.DocumentFormatImage},
	}
	for _, test := range tests {
		if got := InferFormatFromMetadata(test.name, test.contentType); got != test.want {
			t.Errorf("InferFormatFromMetadata(%q, %q) = %q, want %q", test.name, test.contentType, got, test.want)
		}
	}
}
