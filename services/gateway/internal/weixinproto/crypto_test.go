package weixinproto

import (
	"bytes"
	"testing"
)

func TestAESECBPKCS7RoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	for _, plaintext := range [][]byte{
		[]byte(""),
		[]byte("short"),
		[]byte("exactly sixteen!"),
		bytes.Repeat([]byte("x"), 1000),
	} {
		encrypted, err := EncryptAESECBPKCS7(plaintext, key)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		decrypted, err := DecryptAESECBPKCS7(encrypted, key)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatalf("round trip mismatch for %d bytes", len(plaintext))
		}
	}
}

func TestUnpadPKCS7RejectsCorruptPadding(t *testing.T) {
	if _, err := UnpadPKCS7([]byte("0123456789abcde\x05"), 16); err == nil {
		t.Fatal("expected invalid padding error")
	}
	if _, err := UnpadPKCS7([]byte{}, 16); err == nil {
		t.Fatal("expected empty input error")
	}
}
