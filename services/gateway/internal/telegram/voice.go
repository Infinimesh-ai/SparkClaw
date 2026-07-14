package telegram

import (
	"context"
	"encoding/binary"
	"errors"
)

type VoiceTranscriptionRequest struct {
	RequestID  string
	SessionID  string
	Language   string
	PCM16WAV   []byte
	DurationMS int
}

type VoiceTranscriber interface {
	Available(context.Context) error
	Transcribe(context.Context, VoiceTranscriptionRequest) (string, error)
}

type DisabledVoiceTranscriber struct{}

func (DisabledVoiceTranscriber) Available(context.Context) error {
	return NewConnectorError(CodeVoiceUnavailable, false, errors.New("voice transcriber is disabled"))
}

func (DisabledVoiceTranscriber) Transcribe(context.Context, VoiceTranscriptionRequest) (string, error) {
	return "", NewConnectorError(CodeVoiceUnavailable, false, errors.New("voice transcriber is disabled"))
}

func validatePCM16WAV(raw []byte, maxSeconds int) (int, error) {
	if len(raw) < 44 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return 0, errors.New("normalized audio is not a WAV file")
	}
	var audioFormat, channels, bitsPerSample uint16
	var sampleRate uint32
	dataBytes := 0
	for offset := 12; offset+8 <= len(raw); {
		chunkID := string(raw[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		start := offset + 8
		end := start + chunkSize
		if chunkSize < 0 || end > len(raw) {
			return 0, errors.New("normalized WAV chunk is truncated")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return 0, errors.New("normalized WAV format chunk is invalid")
			}
			audioFormat = binary.LittleEndian.Uint16(raw[start : start+2])
			channels = binary.LittleEndian.Uint16(raw[start+2 : start+4])
			sampleRate = binary.LittleEndian.Uint32(raw[start+4 : start+8])
			bitsPerSample = binary.LittleEndian.Uint16(raw[start+14 : start+16])
		case "data":
			dataBytes = chunkSize
		}
		offset = end
		if chunkSize%2 != 0 {
			offset++
		}
	}
	if audioFormat != 1 || channels != 1 || sampleRate != 16000 || bitsPerSample != 16 || dataBytes <= 0 {
		return 0, errors.New("normalized WAV must be mono 16 kHz PCM16")
	}
	durationMS := dataBytes * 1000 / (16000 * 2)
	if maxSeconds > 0 && durationMS > maxSeconds*1000 {
		return 0, errors.New("normalized WAV exceeds the duration limit")
	}
	return durationMS, nil
}
