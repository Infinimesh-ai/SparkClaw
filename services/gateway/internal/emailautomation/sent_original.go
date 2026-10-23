package emailautomation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"golang.org/x/net/html/charset"
)

const maxSentHeaderBytes = 64 << 10

var errSentOriginal = errors.New("Sent original does not match the bound predicate")
var sentDateZone = regexp.MustCompile(`^[+-](?:0[0-9]|1[0-9]|2[0-3])[0-5][0-9]$`)

// Unlike the default capture hot path, an exact Sent query must verify the
// original digest and its headers before returning success. One bounded pass
// covers the original without parsing or changing its body/attachments.
func verifySentOriginal(ctx context.Context, root *os.Root, relative string, size int64, digest, account string, match app.EmailSentMatch) error {
	if err := sentCapturePath(root, relative); err != nil {
		return err
	}
	f, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != size || size < 1 || size > maxCaptureFileBytes {
		return errSentOriginal
	}
	hash := sha256.New()
	reader := bufio.NewReader(io.TeeReader(io.LimitReader(&sentContextReader{ctx, f}, size+1), hash))
	headers, err := readSentHeaders(reader)
	if err != nil {
		return err
	}
	if err := matchSentHeaders(headers, account, match); err != nil {
		return err
	}
	if _, err = io.Copy(io.Discard, reader); err != nil {
		return err
	}
	after, err := f.Stat()
	current, pathErr := root.Stat(relative)
	if err != nil || pathErr != nil || !os.SameFile(stat, current) || sentCapturePath(root, relative) != nil || after.Size() != size || after.ModTime() != stat.ModTime() || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
		return errSentOriginal
	}
	return ctx.Err()
}

type sentContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *sentContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// OpenRoot prevents escape; rejecting every symlink also prevents a different
// in-workspace original from being substituted behind a canonical source path.
func sentCapturePath(root *os.Root, relative string) error {
	parts := strings.Split(relative, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errSentOriginal
		}
	}
	return nil
}

// Keep unstructured header whitespace. net/textproto trims values, which could
// turn a differing Subject into an apparent exact match. Only one conventional
// SP after ':' is separator syntax; folding removes CRLF but retains its WSP.
func readSentHeaders(reader *bufio.Reader) (map[string][]string, error) {
	headers := map[string][]string{}
	last, total := "", 0
	for {
		raw, err := reader.ReadSlice('\n')
		line := string(raw)
		total += len(line)
		if err != nil || total > maxSentHeaderBytes || len(line) > 1000 || !strings.HasSuffix(line, "\n") {
			return nil, errSentOriginal
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return headers, nil
		}
		for _, b := range []byte(line) {
			if b < 32 && b != '\t' || b == 127 {
				return nil, errSentOriginal
			}
		}
		if line[0] == ' ' || line[0] == '\t' {
			if last == "" {
				return nil, errSentOriginal
			}
			values := headers[last]
			values[len(values)-1] += line
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 1 {
			return nil, errSentOriginal
		}
		for _, b := range []byte(line[:colon]) {
			if b < 33 || b > 126 {
				return nil, errSentOriginal
			}
		}
		last = strings.ToLower(line[:colon])
		value := strings.TrimPrefix(line[colon+1:], " ")
		headers[last] = append(headers[last], value)
	}
}

func matchSentHeaders(headers map[string][]string, account string, match app.EmailSentMatch) error {
	if match.Validate() != nil || !app.ValidEmailSentMatchAddress(account) || len(headers["cc"]) != 0 || len(headers["bcc"]) != 0 {
		return errSentOriginal
	}
	for _, key := range []string{"subject", "from", "to", "date"} {
		if len(headers[key]) != 1 || headers[key][0] == "" {
			return errSentOriginal
		}
	}
	decoder := &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	subject, err := decoder.DecodeHeader(headers["subject"][0])
	if err != nil || !utf8.ValidString(subject) || strings.Contains(subject, "=?") || strings.ContainsRune(subject, utf8.RuneError) || subject != match.Subject {
		return errSentOriginal
	}
	parser := mail.AddressParser{WordDecoder: decoder}
	for key, expected := range map[string]string{"from": account, "to": match.Recipient} {
		value := headers[key][0]
		// Groups are not a single mailbox even when they contain one member.
		if strings.ContainsAny(value, ":;") {
			return errSentOriginal
		}
		addresses, err := parser.ParseList(value)
		if err != nil || len(addresses) != 1 || strings.Contains(addresses[0].Name, "=?") || !utf8.ValidString(addresses[0].Name) || strings.ContainsRune(addresses[0].Name, utf8.RuneError) || !sameSentAddress(addresses[0].Address, expected) {
			return errSentOriginal
		}
	}
	date, err := mail.ParseDate(headers["date"][0])
	start, _ := time.Parse(time.RFC3339, match.IntervalStart)
	end, _ := time.Parse(time.RFC3339, match.IntervalEnd)
	// An unknown/military timezone is not an explicit, trustworthy UTC instant.
	zone := strings.Fields(headers["date"][0])
	last := ""
	if len(zone) > 0 {
		last = zone[len(zone)-1]
	}
	knownZone := sentDateZone.MatchString(last) && last != "-0000" || last == "GMT" || last == "UT"
	if err != nil || !knownZone || date.Before(start) || !date.Before(end) {
		return errSentOriginal
	}
	return nil
}

// No alias, plus-tag or dot normalization. Domain spelling is case-insensitive;
// the local part is preserved exactly, including for Gmail.
func sameSentAddress(actual, expected string) bool {
	if !app.ValidEmailSentMatchAddress(actual) || !app.ValidEmailSentMatchAddress(expected) {
		return false
	}
	a, b := strings.LastIndexByte(actual, '@'), strings.LastIndexByte(expected, '@')
	return actual[:a] == expected[:b] && strings.EqualFold(actual[a+1:], expected[b+1:])
}
