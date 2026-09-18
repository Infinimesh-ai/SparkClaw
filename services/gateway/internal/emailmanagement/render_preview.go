package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	xhtml "golang.org/x/net/html"
)

const (
	emailRenderSanitizerVersion = "structured-mail-v3"
	maxEmailRenderArtifactBytes = 2 << 20
	maxEmailRenderDOMNodes      = 50000
	maxEmailRenderDepth         = 128
	maxEmailRenderURLBytes      = 8192
)

// RenderPreviewNode is a closed, presentation-independent tree. Sender HTML,
// CSS and event attributes never cross the API boundary. Only validated,
// user-clicked HTTP(S) actions retain a URL; WebChat maps known node kinds to
// its own components and styles and never requests those URLs while rendering.
type RenderPreviewNode struct {
	Kind     string              `json:"kind"`
	Text     string              `json:"text,omitempty"`
	Source   string              `json:"source,omitempty"`
	URL      string              `json:"url,omitempty"`
	Alt      string              `json:"alt,omitempty"`
	Level    int                 `json:"level,omitempty"`
	Ordered  bool                `json:"ordered,omitempty"`
	ColSpan  int                 `json:"col_span,omitempty"`
	RowSpan  int                 `json:"row_span,omitempty"`
	Children []RenderPreviewNode `json:"children,omitempty"`
}

type emailRenderDocument struct {
	Version string              `json:"version"`
	Content []RenderPreviewNode `json:"content"`
}

type RenderPreviewView struct {
	ID                    string              `json:"id"`
	Version               int64               `json:"version"`
	State                 string              `json:"state"`
	SanitizerVersion      string              `json:"sanitizer_version"`
	RepresentationID      string              `json:"representation_id"`
	Content               []RenderPreviewNode `json:"content"`
	EmbeddedResourceCount int                 `json:"embedded_resource_count"`
	EmbeddedResourceBytes int64               `json:"embedded_resource_bytes"`
	FailureCode           string              `json:"failure_code,omitempty"`
}

var skippedEmailRenderElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"title": true, "meta": true, "link": true, "base": true,
	"iframe": true, "frame": true, "frameset": true, "object": true,
	"embed": true, "form": true, "input": true, "button": true,
	"select": true, "option": true, "textarea": true, "canvas": true,
	"svg": true, "math": true, "audio": true, "video": true, "source": true,
}

var emailRenderNodeKinds = map[string]bool{
	"text": true, "section": true, "paragraph": true, "heading": true,
	"strong": true, "emphasis": true, "underline": true, "strike": true,
	"small": true, "mark": true, "code": true, "preformatted": true,
	"quote": true, "list": true, "item": true, "description_list": true,
	"term": true, "description": true, "table": true, "table_head": true,
	"table_body": true, "table_foot": true, "row": true, "header_cell": true,
	"cell": true, "image": true, "line_break": true, "divider": true,
	"link": true,
}

var emailRenderPlainURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

func normalizeCID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToLower(value), "cid:")
	return strings.Trim(value, "<>")
}

func prepareEmailRenderDocument(raw string, inline map[string]string) (emailRenderDocument, string) {
	document := emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{}}
	if len(raw) > maxMIMEBodyBytes {
		return document, "preview_html_too_large"
	}
	parsed, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return document, "preview_html_invalid"
	}
	body := parsed
	var findBody func(*xhtml.Node)
	findBody = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "body") {
			body = node
			return
		}
		for child := node.FirstChild; child != nil && body == parsed; child = child.NextSibling {
			findBody(child)
		}
	}
	findBody(parsed)
	nodes, failure := extractEmailRenderChildren(body, inline, 0, false)
	if failure != "" {
		return document, failure
	}
	document.Content = organizeEmailRenderBlocks(nodes)
	if len(document.Content) == 0 {
		return document, "preview_content_empty"
	}
	if err := validateEmailRenderDocument(document); err != nil {
		return emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{}}, "preview_content_invalid"
	}
	rawDocument, err := json.Marshal(document)
	if err != nil || len(rawDocument) > maxEmailRenderArtifactBytes {
		return emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{}}, "preview_output_too_large"
	}
	return document, ""
}

func extractEmailRenderChildren(parent *xhtml.Node, inline map[string]string, depth int, preserveWhitespace bool) ([]RenderPreviewNode, string) {
	if depth > maxEmailRenderDepth {
		return nil, "preview_dom_too_deep"
	}
	result := []RenderPreviewNode{}
	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		nodes, failure := extractEmailRenderNode(child, inline, depth+1, preserveWhitespace)
		if failure != "" {
			return nil, failure
		}
		result = append(result, nodes...)
		if len(result) > maxEmailRenderDOMNodes {
			return nil, "preview_dom_too_large"
		}
	}
	return trimEmailRenderWhitespace(result, preserveWhitespace), ""
}

func extractEmailRenderNode(node *xhtml.Node, inline map[string]string, depth int, preserveWhitespace bool) ([]RenderPreviewNode, string) {
	if depth > maxEmailRenderDepth {
		return nil, "preview_dom_too_deep"
	}
	if node.Type == xhtml.TextNode {
		value := strings.ReplaceAll(node.Data, "\x00", "")
		if value == "" {
			return nil, ""
		}
		return []RenderPreviewNode{{Kind: "text", Text: value}}, ""
	}
	if node.Type != xhtml.ElementNode {
		return nil, ""
	}
	tag := strings.ToLower(node.Data)
	if skippedEmailRenderElements[tag] || emailRenderElementHidden(node) {
		return nil, ""
	}
	if tag == "img" {
		for _, attr := range node.Attr {
			if strings.EqualFold(attr.Key, "src") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(attr.Val)), "cid:") {
				if resource, ok := inline[normalizeCID(attr.Val)]; ok && validEmailRenderImage(resource) {
					return []RenderPreviewNode{{Kind: "image", Source: resource, Alt: emailRenderAttribute(node, "alt")}}, ""
				}
			}
		}
		// Remote, missing and invalid images disappear completely. Keeping a
		// source-less img would make browsers show a broken-image placeholder.
		return nil, ""
	}
	if tag == "br" {
		return []RenderPreviewNode{{Kind: "line_break"}}, ""
	}
	if tag == "hr" {
		return []RenderPreviewNode{{Kind: "divider"}}, ""
	}
	if tag == "a" {
		children, failure := extractEmailRenderChildren(node, inline, depth, preserveWhitespace)
		if failure != "" {
			return nil, failure
		}
		if !emailRenderNodesHaveContent(children) {
			if label := emailRenderActionLabel(node); label != "" {
				children = []RenderPreviewNode{{Kind: "text", Text: label}}
			}
		}
		href, valid := normalizeEmailRenderURL(emailRenderAttribute(node, "href"))
		if !valid || !emailRenderNodesHaveContent(children) {
			// Unsafe, relative or empty actions are not clickable, but readable
			// descendants remain part of the message.
			return children, ""
		}
		return []RenderPreviewNode{{Kind: "link", URL: href, Children: children}}, ""
	}
	kind := emailRenderKind(tag)
	children, failure := extractEmailRenderChildren(node, inline, depth, preserveWhitespace || kind == "preformatted")
	if failure != "" {
		return nil, failure
	}
	if len(children) == 0 || !emailRenderNodesHaveContent(children) {
		return nil, ""
	}
	result := RenderPreviewNode{Kind: kind, Children: children}
	switch kind {
	case "heading":
		result.Level, _ = strconv.Atoi(strings.TrimPrefix(tag, "h"))
		if result.Level < 1 || result.Level > 6 {
			result.Level = 2
		}
	case "list":
		result.Ordered = tag == "ol"
	case "cell", "header_cell":
		result.ColSpan = emailRenderSpan(node, "colspan")
		result.RowSpan = emailRenderSpan(node, "rowspan")
	}
	return []RenderPreviewNode{result}, ""
}

func emailRenderKind(tag string) string {
	switch tag {
	case "p":
		return "paragraph"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "heading"
	case "strong", "b":
		return "strong"
	case "em", "i":
		return "emphasis"
	case "u", "ins":
		return "underline"
	case "s", "strike", "del":
		return "strike"
	case "small":
		return "small"
	case "mark":
		return "mark"
	case "code", "kbd", "samp":
		return "code"
	case "pre":
		return "preformatted"
	case "blockquote":
		return "quote"
	case "ul", "ol":
		return "list"
	case "li":
		return "item"
	case "dl":
		return "description_list"
	case "dt":
		return "term"
	case "dd":
		return "description"
	case "table":
		return "table"
	case "thead":
		return "table_head"
	case "tbody":
		return "table_body"
	case "tfoot":
		return "table_foot"
	case "tr":
		return "row"
	case "th":
		return "header_cell"
	case "td":
		return "cell"
	default:
		// Unknown layout wrappers become neutral sections. Their attributes and
		// styles are intentionally discarded while readable descendants stay.
		return "section"
	}
}

func emailRenderElementHidden(node *xhtml.Node) bool {
	for _, attr := range node.Attr {
		name, value := strings.ToLower(attr.Key), strings.ToLower(strings.TrimSpace(attr.Val))
		if name == "hidden" || (name == "aria-hidden" && value == "true") {
			return true
		}
		if name == "style" {
			compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(value)
			if strings.Contains(compact, "display:none") || strings.Contains(compact, "visibility:hidden") || strings.Contains(compact, "opacity:0") {
				return true
			}
		}
	}
	return false
}

func emailRenderAttribute(node *xhtml.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return strings.TrimSpace(strings.ReplaceAll(attr.Val, "\x00", ""))
		}
	}
	return ""
}

func emailRenderSpan(node *xhtml.Node, name string) int {
	value, err := strconv.Atoi(emailRenderAttribute(node, name))
	if err != nil || value < 2 || value > 20 {
		return 0
	}
	return value
}

func validEmailRenderImage(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "data:image/png;base64,") || strings.HasPrefix(lower, "data:image/jpeg;base64,") || strings.HasPrefix(lower, "data:image/webp;base64,")
}

func normalizeEmailRenderURL(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if value == "" || len(value) > maxEmailRenderURLBytes {
		return "", false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	return parsed.String(), true
}

func emailRenderActionLabel(node *xhtml.Node) string {
	for _, name := range []string{"aria-label", "title"} {
		if value := boundedEmailRenderLabel(emailRenderAttribute(node, name)); value != "" {
			return value
		}
	}
	var findImageAlt func(*xhtml.Node) string
	findImageAlt = func(current *xhtml.Node) string {
		if current.Type == xhtml.ElementNode && emailRenderElementHidden(current) {
			return ""
		}
		if current.Type == xhtml.ElementNode && strings.EqualFold(current.Data, "img") {
			return boundedEmailRenderLabel(emailRenderAttribute(current, "alt"))
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if value := findImageAlt(child); value != "" {
				return value
			}
		}
		return ""
	}
	return findImageAlt(node)
}

func boundedEmailRenderLabel(value string) string {
	value = strings.Join(strings.Fields(strings.ReplaceAll(value, "\x00", "")), " ")
	characters := []rune(value)
	if len(characters) > 200 {
		characters = characters[:200]
	}
	return string(characters)
}

func linkifyPlainEmailText(value string) []RenderPreviewNode {
	result := []RenderPreviewNode{}
	cursor := 0
	for _, location := range emailRenderPlainURL.FindAllStringIndex(value, -1) {
		if location[0] > cursor {
			result = append(result, RenderPreviewNode{Kind: "text", Text: value[cursor:location[0]]})
		}
		candidate := value[location[0]:location[1]]
		trimmed := strings.TrimRight(candidate, ".,;:!?)]}")
		suffix := candidate[len(trimmed):]
		if href, valid := normalizeEmailRenderURL(trimmed); valid {
			result = append(result, RenderPreviewNode{Kind: "link", URL: href, Children: []RenderPreviewNode{{Kind: "text", Text: trimmed}}})
		} else {
			result = append(result, RenderPreviewNode{Kind: "text", Text: trimmed})
		}
		if suffix != "" {
			result = append(result, RenderPreviewNode{Kind: "text", Text: suffix})
		}
		cursor = location[1]
	}
	if cursor < len(value) {
		result = append(result, RenderPreviewNode{Kind: "text", Text: value[cursor:]})
	}
	if len(result) == 0 && value != "" {
		result = append(result, RenderPreviewNode{Kind: "text", Text: value})
	}
	return result
}

func trimEmailRenderWhitespace(nodes []RenderPreviewNode, preserve bool) []RenderPreviewNode {
	if preserve {
		return nodes
	}
	for len(nodes) > 0 && nodes[0].Kind == "text" && strings.TrimSpace(nodes[0].Text) == "" {
		nodes = nodes[1:]
	}
	for len(nodes) > 0 && nodes[len(nodes)-1].Kind == "text" && strings.TrimSpace(nodes[len(nodes)-1].Text) == "" {
		nodes = nodes[:len(nodes)-1]
	}
	result := make([]RenderPreviewNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == "text" && strings.TrimSpace(node.Text) == "" {
			node.Text = " "
			if len(result) > 0 && result[len(result)-1].Kind == "text" && strings.TrimSpace(result[len(result)-1].Text) == "" {
				continue
			}
		}
		result = append(result, node)
	}
	return result
}

func emailRenderNodesHaveContent(nodes []RenderPreviewNode) bool {
	for _, node := range nodes {
		if node.Kind == "image" || node.Kind == "divider" || node.Kind == "line_break" || strings.TrimSpace(node.Text) != "" || emailRenderNodesHaveContent(node.Children) {
			return true
		}
	}
	return false
}

func organizeEmailRenderBlocks(nodes []RenderPreviewNode) []RenderPreviewNode {
	result := []RenderPreviewNode{}
	inline := []RenderPreviewNode{}
	flush := func() {
		inline = trimEmailRenderWhitespace(inline, false)
		if emailRenderNodesHaveContent(inline) {
			result = append(result, RenderPreviewNode{Kind: "paragraph", Children: inline})
		}
		inline = nil
	}
	for _, node := range nodes {
		if emailRenderBlockKind(node.Kind) {
			flush()
			result = append(result, node)
		} else {
			inline = append(inline, node)
		}
	}
	flush()
	return result
}

func emailRenderBlockKind(kind string) bool {
	switch kind {
	case "section", "paragraph", "heading", "preformatted", "quote", "list", "description_list", "table", "divider", "image":
		return true
	default:
		return false
	}
}

func validateEmailRenderDocument(document emailRenderDocument) error {
	if document.Version != emailRenderSanitizerVersion || len(document.Content) == 0 {
		return errors.New("invalid document")
	}
	count := 0
	var walk func([]RenderPreviewNode, int) error
	walk = func(nodes []RenderPreviewNode, depth int) error {
		if depth > maxEmailRenderDepth {
			return errors.New("document too deep")
		}
		for _, node := range nodes {
			count++
			if count > maxEmailRenderDOMNodes || !emailRenderNodeKinds[node.Kind] || strings.ContainsRune(node.Text, '\x00') || strings.ContainsRune(node.Alt, '\x00') {
				return errors.New("invalid node")
			}
			if node.Kind == "image" {
				if !validEmailRenderImage(node.Source) || len(node.Children) != 0 {
					return errors.New("invalid image")
				}
			} else if node.Source != "" {
				return errors.New("unexpected source")
			}
			if node.Kind == "link" {
				if href, valid := normalizeEmailRenderURL(node.URL); !valid || href != node.URL || !emailRenderNodesHaveContent(node.Children) {
					return errors.New("invalid link")
				}
			} else if node.URL != "" {
				return errors.New("unexpected url")
			}
			if node.Kind == "heading" && (node.Level < 1 || node.Level > 6) {
				return errors.New("invalid heading")
			}
			if node.ColSpan < 0 || node.ColSpan > 20 || node.RowSpan < 0 || node.RowSpan > 20 {
				return errors.New("invalid table span")
			}
			if err := walk(node.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(document.Content, 0)
}

func buildEmailRenderPreview(ctx context.Context, workspace, owner string, representation app.EmailRepresentation, capture app.EmailCaptureVersion, candidate emailRenderCandidate) (app.EmailRenderPreview, error) {
	preview := app.EmailRenderPreview{MailID: representation.MailID, CaptureID: capture.ID, RepresentationID: representation.ID,
		SourceSHA256: capture.OriginalSHA256, SanitizerVersion: emailRenderSanitizerVersion,
		EmbeddedResourceCount: candidate.EmbeddedResourceCount, EmbeddedResourceBytes: candidate.EmbeddedResourceBytes}
	document := emailRenderDocument{Version: emailRenderSanitizerVersion, Content: []RenderPreviewNode{}}
	failure := ""
	if strings.TrimSpace(candidate.HTML) != "" {
		document, failure = prepareEmailRenderDocument(candidate.HTML, candidate.Inline)
	} else if strings.TrimSpace(candidate.Plain) != "" {
		document.Content = []RenderPreviewNode{{Kind: "preformatted", Children: linkifyPlainEmailText(candidate.Plain)}}
		if err := validateEmailRenderDocument(document); err != nil {
			failure = "preview_content_invalid"
		}
	} else {
		failure = "preview_content_empty"
	}
	if failure != "" {
		preview.State, preview.FailureCode = app.EmailRenderFailed, failure
		return preview, nil
	}
	raw, err := json.Marshal(document)
	if err != nil || len(raw) == 0 || len(raw) > maxEmailRenderArtifactBytes {
		preview.State, preview.FailureCode = app.EmailRenderFailed, "preview_output_too_large"
		return preview, nil
	}
	preview.State = app.EmailRenderReady
	preview.ArtifactPath = path.Join("email", ownerScope(owner), "normalized", representation.ID, "render", emailRenderSanitizerVersion, "content.json")
	preview.ArtifactBytes, preview.ArtifactSHA256 = int64(len(raw)), sourceHash(raw)
	if err := publishBytes(ctx, workspace, preview.ArtifactPath, raw); err != nil {
		return app.EmailRenderPreview{}, err
	}
	manifestPath := path.Join(path.Dir(preview.ArtifactPath), "manifest.json")
	manifest := struct {
		Preview   app.EmailRenderPreview `json:"preview"`
		CreatedAt string                 `json:"created_at"`
	}{preview, emailTime(representation.CreatedAt)}
	if err := publishJSON(ctx, workspace, manifestPath, manifest); err != nil {
		return app.EmailRenderPreview{}, err
	}
	return preview, nil
}

func publishBytes(ctx context.Context, workspace, relative string, raw []byte) error {
	if !safeRelativePath(relative) || len(raw) == 0 || len(raw) > maxEmailRenderArtifactBytes {
		return errors.New("email_output_path_invalid")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(relative), 0700); err != nil {
		return err
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		_, err = readVerifiedFile(ctx, root, sourceFile{Path: relative, SHA256: sourceHash(raw), Bytes: int64(len(raw))}, int64(len(raw)))
		return err
	}
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func (s *Service) RenderPreview(ctx context.Context, owner, mailID string) (RenderPreviewView, error) {
	return stableProjection(ctx, s, owner, func(version int64) (RenderPreviewView, error) {
		out := RenderPreviewView{ID: mailID, Version: version, SanitizerVersion: emailRenderSanitizerVersion, Content: []RenderPreviewNode{}}
		mail, found, err := s.repository.GetEmailMail(ctx, owner, mailID)
		if err != nil {
			return out, err
		}
		if !found {
			return out, ErrNotFound
		}
		out.RepresentationID = mail.RepresentationID
		if mail.RepresentationID == "" {
			out.State, out.FailureCode = app.EmailRenderUnavailable, "preview_source_unavailable"
			return out, nil
		}
		representation, found, err := s.repository.GetEmailRepresentation(ctx, owner, mail.RepresentationID)
		if err != nil {
			return out, err
		}
		if !found || representation.MailID != mail.ID {
			return out, ErrNotFound
		}
		preview, found, err := s.repository.GetEmailRenderPreview(ctx, owner, representation.ID, emailRenderSanitizerVersion)
		if err != nil {
			return out, err
		}
		if !found {
			out.State, out.FailureCode = app.EmailRenderUnavailable, "preview_not_migrated"
			return out, nil
		}
		out.State, out.FailureCode = preview.State, preview.FailureCode
		out.EmbeddedResourceCount, out.EmbeddedResourceBytes = preview.EmbeddedResourceCount, preview.EmbeddedResourceBytes
		if preview.State != app.EmailRenderReady {
			return out, nil
		}
		expectedPrefix := path.Join("email", ownerScope(owner), "normalized", representation.ID, "render", emailRenderSanitizerVersion) + "/"
		if !strings.HasPrefix(preview.ArtifactPath, expectedPrefix) || path.Base(preview.ArtifactPath) != "content.json" || preview.SourceSHA256 == "" {
			return out, ErrInvalidInput
		}
		root, err := os.OpenRoot(s.opts.WorkspaceRoot)
		if err != nil {
			return out, err
		}
		defer root.Close()
		raw, err := readVerifiedFile(ctx, root, sourceFile{Path: preview.ArtifactPath, SHA256: preview.ArtifactSHA256, Bytes: preview.ArtifactBytes}, maxEmailRenderArtifactBytes)
		if err != nil {
			return out, err
		}
		var document emailRenderDocument
		if err := json.Unmarshal(raw, &document); err != nil || validateEmailRenderDocument(document) != nil {
			return out, ErrInvalidInput
		}
		out.Content = document.Content
		return out, nil
	})
}
