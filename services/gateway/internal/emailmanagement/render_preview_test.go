package emailmanagement

import (
	"strings"
	"testing"
)

func TestPrepareEmailRenderDocumentExtractsReadableStructure(t *testing.T) {
	inline := map[string]string{"logo@example.test": "data:image/png;base64,iVBORw0KGgo="}
	raw := `<html><head><style>body{background:url(https://tracker.example/bg)}</style><script>alert(1)</script></head><body onload="steal()">
		<h1 style="font-size:90px">Account notice</h1><table width="600" style="border-collapse:collapse;color:#123456"><tr><td colspan="2">Important code: <strong>632980</strong></td></tr></table>
		<a href="https://login.example/verify?token=abc">Verify account</a><a href="javascript:steal()">Unsafe action text</a><img src="https://tracker.example/pixel" alt="tracking pixel"><img src="cid:logo@example.test" onerror="steal()" alt="Brand">
		<div style="display:none">hidden tracking copy</div><form action="https://phish.example"><input name="secret"></form><iframe srcdoc="unsafe"></iframe></body></html>`
	document, failure := prepareEmailRenderDocument(raw, inline)
	if failure != "" {
		t.Fatalf("failure=%s", failure)
	}
	serialized := renderDocumentDebug(document.Content)
	for _, forbidden := range []string{"tracker.example", "phish.example", "tracking pixel", "hidden tracking copy", "steal", "secret", "style", "href"} {
		if strings.Contains(strings.ToLower(serialized), strings.ToLower(forbidden)) {
			t.Fatalf("projection retained %q: %s", forbidden, serialized)
		}
	}
	for _, expected := range []string{"heading:text:Account notice", "table:", "Important code:", "strong:text:632980", "link:https://login.example/verify?token=abc", "Verify account", "Unsafe action text", "image:Brand"} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("projection lost %q: %s", expected, serialized)
		}
	}
	if got := countRenderKind(document.Content, "image"); got != 1 {
		t.Fatalf("image nodes=%d want 1: %s", got, serialized)
	}
	if got := countRenderKind(document.Content, "link"); got != 1 {
		t.Fatalf("link nodes=%d want 1: %s", got, serialized)
	}
}

func TestPrepareEmailRenderDocumentKeepsClickOnlyActionsWithoutLoadingImages(t *testing.T) {
	document, failure := prepareEmailRenderDocument(`<p>Continue below.</p>
		<a href="https://accounts.example.test/confirm?token=secret"><img src="https://cdn.example.test/button.png" alt="Confirm this account"></a>
		<a href="//unsafe.example.test/path">Relative action</a>
		<a href="https://user:pass@example.test/path">Credential action</a>`, nil)
	if failure != "" {
		t.Fatalf("failure=%s", failure)
	}
	debug := renderDocumentDebug(document.Content)
	if got := countRenderKind(document.Content, "image"); got != 0 {
		t.Fatalf("image nodes=%d want 0: %s", got, debug)
	}
	if got := countRenderKind(document.Content, "link"); got != 1 {
		t.Fatalf("link nodes=%d want 1: %s", got, debug)
	}
	for _, expected := range []string{"https://accounts.example.test/confirm?token=secret", "Confirm this account", "Relative action", "Credential action"} {
		if !strings.Contains(debug, expected) {
			t.Fatalf("projection lost %q: %s", expected, debug)
		}
	}
}

func TestStructuredPlainTextExtractsHTTPSActions(t *testing.T) {
	children := linkifyPlainEmailText("Confirm at https://accounts.example.test/verify?t=abc. Ignore ftp://example.test/file")
	document := emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{{Kind: "preformatted", Children: children}}}
	if err := validateEmailRenderDocument(document); err != nil {
		t.Fatal(err)
	}
	debug := renderDocumentDebug(document.Content)
	if got := countRenderKind(document.Content, "link"); got != 1 {
		t.Fatalf("link nodes=%d want 1: %s", got, debug)
	}
	if !strings.Contains(debug, "https://accounts.example.test/verify?t=abc") || !strings.Contains(debug, "ftp://example.test/file") {
		t.Fatalf("unexpected content: %s", debug)
	}
}

func TestValidateEmailRenderDocumentRejectsUnsafeLink(t *testing.T) {
	document := emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{{Kind: "paragraph", Children: []RenderPreviewNode{{Kind: "link", URL: "javascript:alert(1)", Children: []RenderPreviewNode{{Kind: "text", Text: "Run"}}}}}}}
	if err := validateEmailRenderDocument(document); err == nil {
		t.Fatal("unsafe link passed validation")
	}
}

func TestPrepareEmailRenderDocumentRemovesUnavailableImagesWithoutPlaceholder(t *testing.T) {
	document, failure := prepareEmailRenderDocument(`<p>Hello</p><img src="https://example.test/missing.png" alt="missing banner"><p>World</p>`, nil)
	if failure != "" {
		t.Fatalf("failure=%s", failure)
	}
	if got := countRenderKind(document.Content, "image"); got != 0 {
		t.Fatalf("image nodes=%d want 0", got)
	}
	debug := renderDocumentDebug(document.Content)
	if strings.Contains(debug, "missing banner") || !strings.Contains(debug, "Hello") || !strings.Contains(debug, "World") {
		t.Fatalf("unexpected content: %s", debug)
	}
}

func TestStructuredPlainTextDoesNotInterpretMarkup(t *testing.T) {
	document := emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{{Kind: "preformatted", Children: []RenderPreviewNode{{Kind: "text", Text: "<script>unsafe()</script>\nVerification code: 428731"}}}}}
	if err := validateEmailRenderDocument(document); err != nil {
		t.Fatal(err)
	}
	debug := renderDocumentDebug(document.Content)
	if !strings.Contains(debug, "<script>unsafe()</script>") || !strings.Contains(debug, "428731") {
		t.Fatalf("plain projection changed source text: %s", debug)
	}
}

func TestPrepareEmailRenderDocumentRejectsOversizedDOM(t *testing.T) {
	raw := strings.Repeat("<span>x</span>", maxEmailRenderDOMNodes+1)
	_, failure := prepareEmailRenderDocument(raw, nil)
	if failure != "preview_dom_too_large" {
		t.Fatalf("failure=%q", failure)
	}
}

func countRenderKind(nodes []RenderPreviewNode, kind string) int {
	count := 0
	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
		count += countRenderKind(node.Children, kind)
	}
	return count
}

func renderDocumentDebug(nodes []RenderPreviewNode) string {
	var out strings.Builder
	for _, node := range nodes {
		out.WriteString(node.Kind)
		out.WriteByte(':')
		out.WriteString(node.Text)
		out.WriteString(node.Alt)
		out.WriteString(node.URL)
		out.WriteString(renderDocumentDebug(node.Children))
		out.WriteByte('|')
	}
	return out.String()
}
