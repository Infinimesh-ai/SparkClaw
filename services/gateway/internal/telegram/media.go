package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var outputFilePattern = regexp.MustCompile(`(?:workspace://)?((?:outputs|media)/[A-Za-z0-9._~!$&'()*+,;=:@%/\-]+\.[A-Za-z0-9]{1,8})`)

func (d *Dispatcher) messageAttachments(ctx context.Context, chatSession app.ExternalChatSession, message *Message) ([]app.MessageAttachment, string, error) {
	attachments := []app.MessageAttachment{}
	if len(message.Photo) > 0 {
		photo := largestPhoto(message.Photo)
		attachment, err := d.downloadAttachment(ctx, chatSession, photo.FileID, photo.FileUniqueID+".jpg", "image/jpeg", photo.FileSize, photo.Width, photo.Height, "media")
		if err != nil {
			return nil, "", err
		}
		attachments = append(attachments, attachment)
	}
	if message.Document != nil {
		document := message.Document
		if !allowedTelegramDocument(document.FileName) {
			return nil, "", NewConnectorError(CodeAttachmentUnsupported, false, nil)
		}
		attachment, err := d.downloadAttachment(ctx, chatSession, document.FileID, document.FileName, document.MimeType, document.FileSize, 0, 0, "uploads")
		if err != nil {
			return nil, "", err
		}
		attachments = append(attachments, attachment)
	}
	if message.Audio != nil {
		audio := message.Audio
		name := audio.FileName
		if name == "" {
			name = audio.FileUniqueID + audioExtension(audio.MimeType)
		}
		attachment, err := d.downloadAttachment(ctx, chatSession, audio.FileID, name, audio.MimeType, audio.FileSize, 0, 0, "uploads")
		if err != nil {
			return nil, "", err
		}
		attachments = append(attachments, attachment)
	}
	if len(attachments) > d.channelCfg.MaxAttachments {
		return nil, "", NewConnectorError(CodeAttachmentUnsupported, false, errors.New("attachment count exceeds limit"))
	}
	if message.Voice != nil {
		voiceText, err := d.transcribeVoice(ctx, chatSession, message.Voice, message.MessageID)
		return attachments, voiceText, err
	}
	return attachments, "", nil
}

func (d *Dispatcher) downloadAttachment(ctx context.Context, chatSession app.ExternalChatSession, fileID, name, declaredType string, declaredBytes int64, width, height int, folder string) (attachment app.MessageAttachment, err error) {
	if declaredBytes > d.channelCfg.MaxDownloadBytes {
		return app.MessageAttachment{}, NewConnectorError(CodeAttachmentTooLarge, false, nil)
	}
	remote, err := d.client.GetFile(ctx, fileID)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	if remote.FileSize > d.channelCfg.MaxDownloadBytes {
		return app.MessageAttachment{}, NewConnectorError(CodeAttachmentTooLarge, false, nil)
	}
	if strings.TrimSpace(remote.FilePath) == "" {
		return app.MessageAttachment{}, NewConnectorError(CodeAttachmentUnsupported, false, errors.New("Telegram returned no file path"))
	}
	name = safeTelegramFileName(name)
	if filepath.Ext(name) == "" {
		name += strings.ToLower(filepath.Ext(remote.FilePath))
	}
	if name == "" || name == "." {
		name = "telegram-file"
	}
	relPath := filepath.ToSlash(filepath.Join(folder, "telegram", time.Now().UTC().Format("2006/01"), app.NewID("tg")+"-"+name))
	destination, ok := workspacePath(chatSession.WorkspaceRoot, relPath)
	if !ok {
		return app.MessageAttachment{}, NewConnectorError(CodeAttachmentUnsupported, false, errors.New("attachment path escaped workspace"))
	}
	defer func() {
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	written, err := d.client.DownloadFile(ctx, remote.FilePath, destination, d.channelCfg.MaxDownloadBytes)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	contentType, digest, err := inspectDownloadedFile(destination, declaredType)
	if err != nil {
		return app.MessageAttachment{}, err
	}
	if folder == "uploads" && !allowedDownloadedContent(name, contentType) {
		return app.MessageAttachment{}, NewConnectorError(CodeAttachmentUnsupported, false, nil)
	}
	artifactID := app.NewID("artifact")
	stored, err := d.store.SaveArtifactObject(ctx, app.ArtifactObject{
		ID:          artifactID,
		Kind:        "telegram-upload",
		SessionID:   chatSession.LinkedSessionID,
		Backend:     "filesystem",
		Key:         relPath,
		URI:         "workspace://" + relPath,
		Path:        destination,
		ContentType: contentType,
		Bytes:       int(written),
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return app.MessageAttachment{}, fmt.Errorf("save Telegram attachment metadata: %w", err)
	}
	artifactID = stored.ID
	return app.MessageAttachment{
		ArtifactID:  artifactID,
		Name:        name,
		RelPath:     relPath,
		URI:         "workspace://" + relPath,
		ContentType: contentType,
		Bytes:       int(written),
		Width:       width,
		Height:      height,
		SHA256:      digest,
		Source:      "telegram",
	}, nil
}

func (d *Dispatcher) transcribeVoice(ctx context.Context, chatSession app.ExternalChatSession, voice *Voice, messageID int64) (string, error) {
	if err := d.transcriber.Available(ctx); err != nil {
		return "", NewConnectorError(CodeVoiceUnavailable, false, err)
	}
	if voice.Duration > d.channelCfg.MaxVoiceSeconds || voice.FileSize > d.channelCfg.MaxDownloadBytes {
		return "", NewConnectorError(CodeAttachmentTooLarge, false, nil)
	}
	remote, err := d.client.GetFile(ctx, voice.FileID)
	if err != nil {
		return "", err
	}
	if remote.FileSize > d.channelCfg.MaxDownloadBytes || strings.TrimSpace(remote.FilePath) == "" {
		return "", NewConnectorError(CodeAttachmentTooLarge, false, nil)
	}
	tempDir, err := os.MkdirTemp("", "sparkclaw-telegram-voice-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	ext := strings.ToLower(filepath.Ext(remote.FilePath))
	if ext == "" {
		ext = audioExtension(voice.MimeType)
	}
	rawPath := filepath.Join(tempDir, "input"+ext)
	if _, err := d.client.DownloadFile(ctx, remote.FilePath, rawPath, d.channelCfg.MaxDownloadBytes); err != nil {
		return "", err
	}
	wavPath := filepath.Join(tempDir, "normalized.wav")
	if err := d.normalizer(ctx, rawPath, wavPath, d.channelCfg.MaxVoiceSeconds); err != nil {
		return "", NewConnectorError(CodeVoiceUnavailable, false, err)
	}
	maxWAVBytes := int64(d.channelCfg.MaxVoiceSeconds*32000 + (1 << 20))
	wav, err := readBoundedFile(wavPath, maxWAVBytes)
	if err != nil {
		return "", NewConnectorError(CodeVoiceUnavailable, false, err)
	}
	durationMS, err := validatePCM16WAV(wav, d.channelCfg.MaxVoiceSeconds)
	if err != nil {
		return "", NewConnectorError(CodeVoiceUnavailable, false, err)
	}
	requestID := "tgvoice_" + strconv.FormatInt(messageID, 10)
	recordAudit(ctx, d.store, app.AuditEvent{
		SessionID: chatSession.LinkedSessionID,
		Actor:     "telegram",
		Type:      "telegram.voice.transcription.started",
		Summary:   "Telegram voice transcription started",
		Fields:    map[string]any{"request_id": requestID, "duration_ms": durationMS, "bytes": len(wav)},
	})
	text, err := d.transcriber.Transcribe(ctx, VoiceTranscriptionRequest{
		RequestID:  requestID,
		SessionID:  chatSession.LinkedSessionID,
		PCM16WAV:   wav,
		DurationMS: durationMS,
	})
	if err != nil {
		recordAudit(ctx, d.store, app.AuditEvent{SessionID: chatSession.LinkedSessionID, Actor: "telegram", Type: "telegram.voice.transcription.failed", Summary: "Telegram voice transcription failed", Fields: map[string]any{"request_id": requestID}})
		return "", NewConnectorError(CodeVoiceUnavailable, false, err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", NewConnectorError(CodeVoiceUnavailable, false, errors.New("voice transcript is empty"))
	}
	recordAudit(ctx, d.store, app.AuditEvent{SessionID: chatSession.LinkedSessionID, Actor: "telegram", Type: "telegram.voice.transcription.completed", Summary: "Telegram voice transcription completed", Fields: map[string]any{"request_id": requestID, "duration_ms": durationMS, "audio_retained": false}})
	return text, nil
}

func normalizeTelegramAudio(ctx context.Context, inputPath, outputPath string, maxSeconds int) error {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("FFmpeg is unavailable")
	}
	// The caller's context comes from the long-lived poll worker and carries
	// no deadline, and -t only caps output duration, not wall clock; bound
	// the subprocess so a stalled decode cannot pin a worker slot forever.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := 60 * time.Second
		if maxSeconds > 0 && time.Duration(2*maxSeconds)*time.Second > timeout {
			timeout = time.Duration(2*maxSeconds) * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-i", inputPath}
	if maxSeconds > 0 {
		args = append(args, "-t", strconv.Itoa(maxSeconds))
	}
	args = append(args, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-y", outputPath)
	var stderr limitedBuffer
	command := exec.CommandContext(ctx, ffmpeg, args...)
	command.Stdout = io.Discard
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("FFmpeg normalization failed: %s", stderr.String())
	}
	return nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := 400 - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			_, _ = b.buffer.Write(value[:remaining])
		} else {
			_, _ = b.buffer.Write(value)
		}
	}
	return len(value), nil
}

func (b *limitedBuffer) String() string {
	message := strings.TrimSpace(b.buffer.String())
	if message == "" {
		return "conversion process failed"
	}
	return message
}

func (d *Dispatcher) sendMediaAnswer(ctx context.Context, chatSession app.ExternalChatSession, chatID, threadID int64, answer string) (bool, error) {
	if mediaPath, ok := telegramMarkdownMediaPath(answer); ok {
		path, found, err := d.resolveWorkspaceOutput(ctx, chatSession, mediaPath)
		if err != nil {
			return true, err
		}
		if found {
			return true, d.sendImageOrDocument(ctx, chatID, threadID, path, "")
		}
	}
	for _, match := range outputFilePattern.FindAllStringSubmatch(answer, -1) {
		if len(match) < 2 {
			continue
		}
		path, ok, err := d.resolveWorkspaceOutput(ctx, chatSession, match[1])
		if err != nil {
			return true, err
		}
		if !ok {
			continue
		}
		caption := trimCaption(answer, 900)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			return true, d.sendImageOrDocument(ctx, chatID, threadID, path, caption)
		case ".ogg", ".mp3", ".m4a":
			_, err := d.client.SendVoice(ctx, chatID, threadID, path, caption)
			return true, err
		default:
			_, err := d.client.SendDocument(ctx, chatID, threadID, path, filepath.Base(path), caption)
			return true, err
		}
	}
	return false, nil
}

func (d *Dispatcher) sendImageOrDocument(ctx context.Context, chatID, threadID int64, path, caption string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() <= maxPhotoUploadBytes {
		_, err = d.client.SendPhoto(ctx, chatID, threadID, path, caption)
		return err
	}
	_, err = d.client.SendDocument(ctx, chatID, threadID, path, filepath.Base(path), caption)
	return err
}

func (d *Dispatcher) resolveWorkspaceOutput(ctx context.Context, chatSession app.ExternalChatSession, target string) (string, bool, error) {
	target = strings.TrimSpace(strings.TrimPrefix(target, "workspace://"))
	if target == "" {
		return "", false, nil
	}
	objects, err := d.store.ListArtifactObjects(ctx, 500)
	if err != nil {
		return "", false, err
	}
	if filepath.IsAbs(target) {
		for _, object := range objects {
			if object.SessionID == chatSession.LinkedSessionID && sameFilePath(object.Path, target) {
				return target, regularFile(target), nil
			}
		}
		return "", false, nil
	}
	relPath := filepath.ToSlash(filepath.Clean(strings.TrimLeft(target, "/")))
	if relPath == "." || strings.HasPrefix(relPath, "../") || (!strings.HasPrefix(relPath, "outputs/") && !strings.HasPrefix(relPath, "media/")) {
		return "", false, nil
	}
	for _, object := range objects {
		if object.SessionID == chatSession.LinkedSessionID && filepath.ToSlash(object.Key) == relPath && regularFile(object.Path) {
			return object.Path, true, nil
		}
	}
	path, ok := workspacePath(chatSession.WorkspaceRoot, relPath)
	return path, ok && regularFile(path), nil
}

func inspectDownloadedFile(path, declaredType string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha256.New()
	prefix := make([]byte, 512)
	read, readErr := file.Read(prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", "", readErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	if _, err := io.Copy(hash, file); err != nil {
		return "", "", err
	}
	contentType := http.DetectContentType(prefix[:read])
	if bytes.HasPrefix(prefix[:read], []byte("MZ")) || bytes.HasPrefix(prefix[:read], []byte{0x7f, 'E', 'L', 'F'}) {
		contentType = "application/x-executable"
	}
	if contentType == "application/octet-stream" && strings.TrimSpace(declaredType) != "" {
		contentType = strings.TrimSpace(declaredType)
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	}
	return contentType, hex.EncodeToString(hash.Sum(nil)), nil
}

func allowedTelegramDocument(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".md", ".csv", ".tsv":
		return true
	default:
		return false
	}
}

func allowedDownloadedContent(name, contentType string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "application/x-executable") || strings.Contains(contentType, "x-dosexec") {
		return false
	}
	return allowedTelegramDocument(name) || (ext != "" && strings.HasPrefix(contentType, "audio/"))
}

func largestPhoto(photos []PhotoSize) PhotoSize {
	largest := photos[0]
	for _, photo := range photos[1:] {
		if photo.Width*photo.Height > largest.Width*largest.Height || photo.FileSize > largest.FileSize {
			largest = photo
		}
	}
	return largest
}

func safeTelegramFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var out strings.Builder
	for _, char := range name {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("._- ", char) {
			out.WriteRune(char)
		} else {
			out.WriteRune('_')
		}
	}
	cleaned := strings.Trim(out.String(), " .")
	if len([]rune(cleaned)) > 120 {
		cleaned = string([]rune(cleaned)[:120])
	}
	return cleaned
}

func audioExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	default:
		return ".audio"
	}
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, errors.New("normalized voice exceeds size limit")
	}
	return raw, nil
}

func telegramMarkdownMediaPath(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if !strings.HasPrefix(answer, "![") || !strings.HasSuffix(answer, ")") {
		return "", false
	}
	index := strings.Index(answer, "](")
	if index < 2 {
		return "", false
	}
	target := strings.TrimSpace(answer[index+2 : len(answer)-1])
	switch strings.ToLower(filepath.Ext(target)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return target, true
	default:
		return "", false
	}
}

func trimCaption(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

func sameFilePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(strings.TrimSpace(left))
	rightAbs, rightErr := filepath.Abs(strings.TrimSpace(right))
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
