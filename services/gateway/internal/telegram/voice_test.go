package telegram

import (
	"context"
	"encoding/binary"
	"testing"
)

func TestDisabledVoiceTranscriberUsesStableFailure(t *testing.T) {
	transcriber := DisabledVoiceTranscriber{}
	if err := transcriber.Available(context.Background()); connectorErrorCode(err) != CodeVoiceUnavailable || isRetryable(err) {
		t.Fatalf("unexpected disabled transcriber error: %v", err)
	}
}

func TestValidatePCM16WAV(t *testing.T) {
	raw := pcm16WAVFixture(16000)
	durationMS, err := validatePCM16WAV(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	if durationMS != 1000 {
		t.Fatalf("duration = %d, want 1000", durationMS)
	}
	if _, err := validatePCM16WAV(raw, 0); err != nil {
		t.Fatal(err)
	}
	raw[22] = 2
	if _, err := validatePCM16WAV(raw, 2); err == nil {
		t.Fatal("expected stereo WAV rejection")
	}
}

func pcm16WAVFixture(samples int) []byte {
	dataBytes := samples * 2
	raw := make([]byte, 44+dataBytes)
	copy(raw[0:4], "RIFF")
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))
	copy(raw[8:12], "WAVE")
	copy(raw[12:16], "fmt ")
	binary.LittleEndian.PutUint32(raw[16:20], 16)
	binary.LittleEndian.PutUint16(raw[20:22], 1)
	binary.LittleEndian.PutUint16(raw[22:24], 1)
	binary.LittleEndian.PutUint32(raw[24:28], 16000)
	binary.LittleEndian.PutUint32(raw[28:32], 32000)
	binary.LittleEndian.PutUint16(raw[32:34], 2)
	binary.LittleEndian.PutUint16(raw[34:36], 16)
	copy(raw[36:40], "data")
	binary.LittleEndian.PutUint32(raw[40:44], uint32(dataBytes))
	return raw
}
