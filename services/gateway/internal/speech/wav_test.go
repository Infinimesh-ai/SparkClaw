package speech

import (
	"encoding/binary"
	"testing"
)

func TestValidatePCM16WAVAcceptsCanonicalAudio(t *testing.T) {
	wav := testWAV(16000, 1, 16, 8000)
	info, err := ValidatePCM16WAV(wav, 60)
	if err != nil {
		t.Fatal(err)
	}
	if info.DurationMS != 500 || info.DataBytes != 16000 {
		t.Fatalf("unexpected WAV info: %#v", info)
	}
}

func TestValidatePCM16WAVRejectsWrongFormatAndDuration(t *testing.T) {
	if _, err := ValidatePCM16WAV(testWAV(48000, 1, 16, 24000), 60); errorCode(err) != CodeUnsupported {
		t.Fatalf("wrong sample rate error = %v", err)
	}
	if _, err := ValidatePCM16WAV(testWAV(16000, 1, 16, 1600), 60); errorCode(err) != CodeTooShort {
		t.Fatalf("short audio error = %v", err)
	}
	if _, err := ValidatePCM16WAV(testWAV(16000, 1, 16, 32000), 1); errorCode(err) != CodeTooLarge {
		t.Fatalf("long audio error = %v", err)
	}
}

func testWAV(sampleRate, channels, bits, sampleFrames int) []byte {
	blockAlign := channels * bits / 8
	dataBytes := sampleFrames * blockAlign
	raw := make([]byte, 44+dataBytes)
	copy(raw[0:4], "RIFF")
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))
	copy(raw[8:12], "WAVE")
	copy(raw[12:16], "fmt ")
	binary.LittleEndian.PutUint32(raw[16:20], 16)
	binary.LittleEndian.PutUint16(raw[20:22], 1)
	binary.LittleEndian.PutUint16(raw[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(raw[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(raw[28:32], uint32(sampleRate*blockAlign))
	binary.LittleEndian.PutUint16(raw[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(raw[34:36], uint16(bits))
	copy(raw[36:40], "data")
	binary.LittleEndian.PutUint32(raw[40:44], uint32(dataBytes))
	return raw
}

func errorCode(err error) string {
	code, _ := ErrorDetails(err)
	return code
}
