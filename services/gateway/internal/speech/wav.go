package speech

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	CanonicalSampleRate = 16000
	CanonicalChannels   = 1
	CanonicalBits       = 16
	MinimumDurationMS   = 300
)

type WAVInfo struct {
	SampleRate int
	Channels   int
	Bits       int
	DataBytes  int
	DurationMS int64
}

func ValidatePCM16WAV(data []byte, maxDurationSeconds int) (WAVInfo, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return WAVInfo{}, NewError(CodeUnsupported, "audio must be a RIFF/WAVE file", false, nil)
	}
	declaredSize := int(binary.LittleEndian.Uint32(data[4:8])) + 8
	if declaredSize != len(data) {
		return WAVInfo{}, NewError(CodeInvalidRequest, "WAV size does not match the request body", false, nil)
	}

	var formatFound, dataFound bool
	var audioFormat, channels, sampleRate, byteRate, blockAlign, bits int
	dataBytes := 0
	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkSize < 0 || chunkEnd < chunkStart || chunkEnd > len(data) {
			return WAVInfo{}, NewError(CodeInvalidRequest, "WAV chunk exceeds the request body", false, nil)
		}
		switch chunkID {
		case "fmt ":
			if formatFound || chunkSize < 16 {
				return WAVInfo{}, NewError(CodeInvalidRequest, "WAV must contain one valid fmt chunk", false, nil)
			}
			formatFound = true
			audioFormat = int(binary.LittleEndian.Uint16(data[chunkStart : chunkStart+2]))
			channels = int(binary.LittleEndian.Uint16(data[chunkStart+2 : chunkStart+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[chunkStart+4 : chunkStart+8]))
			byteRate = int(binary.LittleEndian.Uint32(data[chunkStart+8 : chunkStart+12]))
			blockAlign = int(binary.LittleEndian.Uint16(data[chunkStart+12 : chunkStart+14]))
			bits = int(binary.LittleEndian.Uint16(data[chunkStart+14 : chunkStart+16]))
		case "data":
			if dataFound {
				return WAVInfo{}, NewError(CodeInvalidRequest, "WAV must contain one data chunk", false, nil)
			}
			dataFound = true
			dataBytes = chunkSize
		}
		offset = chunkEnd
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if !formatFound || !dataFound {
		return WAVInfo{}, NewError(CodeInvalidRequest, "WAV fmt and data chunks are required", false, nil)
	}
	if audioFormat != 1 || channels != CanonicalChannels || sampleRate != CanonicalSampleRate || bits != CanonicalBits {
		return WAVInfo{}, NewError(CodeUnsupported, "audio must be 16 kHz mono PCM16 WAV", false, nil)
	}
	expectedBlockAlign := channels * bits / 8
	expectedByteRate := sampleRate * expectedBlockAlign
	if blockAlign != expectedBlockAlign || byteRate != expectedByteRate || dataBytes%blockAlign != 0 {
		return WAVInfo{}, NewError(CodeInvalidRequest, "WAV PCM metadata is inconsistent", false, nil)
	}
	if dataBytes == 0 {
		return WAVInfo{}, NewError(CodeTooShort, "audio is empty", false, nil)
	}
	durationMS := int64(dataBytes) * 1000 / int64(byteRate)
	if durationMS < MinimumDurationMS {
		return WAVInfo{}, NewError(CodeTooShort, fmt.Sprintf("audio must be at least %d ms", MinimumDurationMS), false, nil)
	}
	if maxDurationSeconds <= 0 {
		return WAVInfo{}, errors.New("maximum audio duration must be positive")
	}
	if durationMS > int64(maxDurationSeconds)*1000 {
		return WAVInfo{}, NewError(CodeTooLarge, fmt.Sprintf("audio exceeds the %d second limit", maxDurationSeconds), false, nil)
	}
	return WAVInfo{
		SampleRate: sampleRate,
		Channels:   channels,
		Bits:       bits,
		DataBytes:  dataBytes,
		DurationMS: durationMS,
	}, nil
}
