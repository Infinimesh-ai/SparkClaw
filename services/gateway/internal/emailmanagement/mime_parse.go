package emailmanagement

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

const (
	maxMIMEBodyBytes       = 2 << 20
	maxMIMEAttachmentBytes = 25 << 20
	maxMIMEAttachments     = 20
	maxMIMEAttachmentTotal = 100 << 20
	maxMIMEDepth           = 16
)

type hashingReader struct {
	reader io.Reader
	hash   hash.Hash
	bytes  int64
}

func newHashingReader(reader io.Reader) *hashingReader {
	return &hashingReader{reader: reader, hash: sha256.New()}
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
		r.bytes += int64(n)
	}
	return n, err
}

func (r *hashingReader) sum() string {
	return "sha256:" + hex.EncodeToString(r.hash.Sum(nil))
}

type mimeParseState struct {
	ctx             context.Context
	root            *os.Root
	representation  *app.EmailRepresentation
	attachmentRoot  string
	attachmentCount int
	attachmentBytes int64
	partial         bool
	plainBodies     []string
	htmlBodies      []string
	decoder         *mime.WordDecoder
}

func parseMIMEOriginal(ctx context.Context, workspace string, ref sourceFile, representation *app.EmailRepresentation) error {
	if ref.Bytes < 1 || ref.Bytes > 110<<20 || !safeRelativePath(ref.Path) {
		return errors.New("email_source_invalid")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(ref.Path)
	if err != nil {
		return errors.New("email_source_missing")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != ref.Bytes {
		return errors.New("email_source_invalid")
	}
	hashed := newHashingReader(contextReader{ctx, file})
	message, parseErr := mail.ReadMessage(bufio.NewReader(hashed))
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, hashed)
		return errors.New("email_source_mime_invalid")
	}
	state := &mimeParseState{ctx: ctx, root: root, representation: representation,
		attachmentRoot: path.Join("email", ownerScopeFromRepresentation(representation), "normalized", representation.ID, "attachments")}
	state.decoder = &mime.WordDecoder{CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		return decodedCharsetReader(charset, input)
	}}
	state.readHeaders(message.Header)
	walkErr := state.walk(textproto.MIMEHeader(message.Header), message.Body, 0)
	_, drainErr := io.Copy(io.Discard, hashed)
	if walkErr != nil {
		return walkErr
	}
	if drainErr != nil || hashed.bytes != ref.Bytes || hashed.sum() != ref.SHA256 {
		return errors.New("email_source_integrity")
	}
	body := strings.TrimSpace(strings.Join(state.plainBodies, "\n\n"))
	if body == "" && len(state.htmlBodies) > 0 {
		body = strings.TrimSpace(htmlText(strings.Join(state.htmlBodies, "\n")))
	}
	if !utf8.ValidString(body) || len(body) > maxMIMEBodyBytes {
		return errors.New("email_source_body_invalid")
	}
	representation.BodyText = body
	if state.partial {
		representation.State = app.EmailParsePartial
		representation.Coverage = "attachment_source_incomplete"
	}
	return ctx.Err()
}

// The representation path already includes the owner digest. Derive it from
// the capture-independent ID namespace passed by the caller through a small
// private header signal, then remove that signal before publication.
func ownerScopeFromRepresentation(representation *app.EmailRepresentation) string {
	if representation.HeaderSignals != nil {
		if value := representation.HeaderSignals["_owner_scope"]; value != "" {
			delete(representation.HeaderSignals, "_owner_scope")
			return value
		}
	}
	return "invalid-owner-scope"
}

func (s *mimeParseState) readHeaders(headers mail.Header) {
	decode := func(value string) string {
		decoded, err := s.decoder.DecodeHeader(strings.TrimSpace(value))
		if err != nil {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(decoded)
	}
	addresses := func(key string) []string {
		parser := mail.AddressParser{WordDecoder: s.decoder}
		values, err := parser.ParseList(headers.Get(key))
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(values))
		for _, address := range values {
			if address.Address != "" {
				out = append(out, address.Address)
			}
		}
		return out
	}
	s.representation.Subject = decode(headers.Get("Subject"))
	s.representation.From = addresses("From")
	s.representation.To = addresses("To")
	s.representation.CC = addresses("Cc")
	s.representation.ReplyTo = addresses("Reply-To")
	s.representation.MessageID = strings.TrimSpace(headers.Get("Message-Id"))
	if date, err := mail.ParseDate(headers.Get("Date")); err == nil {
		s.representation.SourceTime = date.UTC()
	}
	refs := strings.Fields(headers.Get("References"))
	if parent := strings.TrimSpace(headers.Get("In-Reply-To")); parent != "" {
		refs = append(refs, parent)
	}
	s.representation.ReplyReferences = refs
	for _, key := range []string{"Auto-Submitted", "List-Id", "Precedence", "X-Auto-Response-Suppress"} {
		if value, _ := boundedUTF8(strings.TrimSpace(headers.Get(key)), 512); value != "" {
			s.representation.HeaderSignals[key] = value
		}
	}
}

func (s *mimeParseState) walk(headers textproto.MIMEHeader, body io.Reader, depth int) error {
	if depth > maxMIMEDepth {
		return errors.New("email_source_mime_depth")
	}
	mediaType, params, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return errors.New("email_source_mime_invalid")
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				return nil
			}
			if nextErr != nil {
				return errors.New("email_source_mime_invalid")
			}
			if err := s.walk(part.Header, part, depth+1); err != nil {
				part.Close()
				return err
			}
			part.Close()
		}
	}
	decoded := transferDecoded(headers.Get("Content-Transfer-Encoding"), body)
	disposition, dispositionParams, _ := mime.ParseMediaType(headers.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if filename != "" {
		if decodedName, decodeErr := s.decoder.DecodeHeader(filename); decodeErr == nil {
			filename = decodedName
		}
	}
	if strings.EqualFold(disposition, "attachment") || filename != "" {
		return s.attachment(mediaType, filename, decoded)
	}
	if mediaType == "message/rfc822" {
		nested, err := mail.ReadMessage(bufio.NewReader(decoded))
		if err != nil {
			return errors.New("email_source_mime_invalid")
		}
		return s.walk(textproto.MIMEHeader(nested.Header), nested.Body, depth+1)
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		_, err := io.Copy(io.Discard, decoded)
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(decoded, maxMIMEBodyBytes+1))
	if err != nil || len(raw) > maxMIMEBodyBytes {
		return errors.New("email_source_body_invalid")
	}
	decodedBody, err := decodeCharsetBytes(params["charset"], raw)
	if err != nil || !utf8.Valid(decodedBody) {
		return errors.New("email_source_body_invalid")
	}
	if mediaType == "text/plain" {
		s.plainBodies = append(s.plainBodies, string(decodedBody))
	} else {
		s.htmlBodies = append(s.htmlBodies, string(decodedBody))
	}
	return nil
}

func (s *mimeParseState) attachment(mediaType, filename string, decoded io.Reader) error {
	index := s.attachmentCount
	s.attachmentCount++
	attachment := app.EmailAttachment{ID: fmt.Sprintf("part_%d", index), Name: safeAttachmentName(filename), MIMEType: mediaType, State: "skipped_limit"}
	if index >= maxMIMEAttachments {
		s.partial = true
		_, err := io.Copy(io.Discard, decoded)
		s.representation.Attachments = append(s.representation.Attachments, attachment)
		return err
	}
	relative := path.Join(s.attachmentRoot, attachment.ID, attachment.Name)
	if !safeRelativePath(relative) {
		return errors.New("email_output_path_invalid")
	}
	if err := s.root.MkdirAll(path.Dir(relative), 0700); err != nil {
		return err
	}
	file, err := s.root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, digest), io.LimitReader(decoded, maxMIMEAttachmentBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = s.root.Remove(relative)
		return errors.Join(copyErr, closeErr)
	}
	if written > maxMIMEAttachmentBytes || s.attachmentBytes > maxMIMEAttachmentTotal-written {
		_ = s.root.Remove(relative)
		s.partial = true
		attachment.SizeBytes = written
		s.representation.Attachments = append(s.representation.Attachments, attachment)
		_, err = io.Copy(io.Discard, decoded)
		return err
	}
	s.attachmentBytes += written
	attachment.Path = relative
	attachment.SizeBytes = written
	attachment.SHA256 = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	attachment.State = "not_analyzed"
	s.representation.Attachments = append(s.representation.Attachments, attachment)
	return nil
}

func transferDecoded(value string, body io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

func decodedCharsetReader(name string, input io.Reader) (io.Reader, error) {
	encoding := charsetEncoding(name)
	if encoding == nil {
		if name == "" || strings.EqualFold(name, "utf-8") || strings.EqualFold(name, "us-ascii") {
			return input, nil
		}
		return nil, fmt.Errorf("unsupported charset %q", name)
	}
	return transform.NewReader(input, encoding.NewDecoder()), nil
}

func decodeCharsetBytes(name string, raw []byte) ([]byte, error) {
	reader, err := decodedCharsetReader(name, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func charsetEncoding(name string) encoding.Encoding {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "iso-8859-1", "latin1", "latin-1":
		return charmap.ISO8859_1
	case "windows-1252", "cp1252":
		return charmap.Windows1252
	case "gbk", "cp936", "gb2312", "gb_2312", "gb_2312-80", "csgb2312", "csiso58gb231280", "iso-ir-58":
		// Legacy Chinese MIME labels use the compatible GBK decoder. Keep the
		// raw source unchanged; decode only normalized header/body text.
		return simplifiedchinese.GBK
	case "gb18030":
		return simplifiedchinese.GB18030
	case "big5":
		return traditionalchinese.Big5
	case "shift_jis", "shift-jis", "sjis":
		return japanese.ShiftJIS
	case "euc-jp":
		return japanese.EUCJP
	case "iso-2022-jp":
		return japanese.ISO2022JP
	case "euc-kr":
		return korean.EUCKR
	default:
		return nil
	}
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func htmlText(value string) string {
	return html.UnescapeString(htmlTagPattern.ReplaceAllString(value, " "))
}

func safeAttachmentName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "attachment"
	}
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\\`, r) {
			return '_'
		}
		return r
	}, value)
	for len([]byte(value)) > 200 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	value = strings.TrimLeft(value, ".")
	if value == "" {
		return "attachment"
	}
	return value
}
