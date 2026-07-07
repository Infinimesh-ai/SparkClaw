package weixinproto

import (
	"crypto/aes"
	"errors"
	"fmt"
)

// The provider's media payloads are encrypted with AES-ECB + PKCS7. ECB is
// protocol-mandated by the iLink API — do not "upgrade" these helpers to
// CBC/GCM without a corresponding provider change.

func EncryptAESECBPKCS7(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := PadPKCS7(plaintext, aes.BlockSize)
	out := make([]byte, len(padded))
	for start := 0; start < len(padded); start += aes.BlockSize {
		block.Encrypt(out[start:start+aes.BlockSize], padded[start:start+aes.BlockSize])
	}
	return out, nil
}

func DecryptAESECBPKCS7(ciphertext, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("AES-ECB ciphertext length must be a positive multiple of %d", aes.BlockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ciphertext))
	for start := 0; start < len(ciphertext); start += aes.BlockSize {
		block.Decrypt(out[start:start+aes.BlockSize], ciphertext[start:start+aes.BlockSize])
	}
	return UnpadPKCS7(out, aes.BlockSize)
}

func PadPKCS7(in []byte, blockSize int) []byte {
	pad := blockSize - len(in)%blockSize
	out := make([]byte, len(in)+pad)
	copy(out, in)
	for i := len(in); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func UnpadPKCS7(in []byte, blockSize int) ([]byte, error) {
	if len(in) == 0 || len(in)%blockSize != 0 {
		return nil, errors.New("invalid PKCS7 padded data length")
	}
	pad := int(in[len(in)-1])
	if pad == 0 || pad > blockSize || pad > len(in) {
		return nil, errors.New("invalid PKCS7 padding")
	}
	for _, b := range in[len(in)-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid PKCS7 padding bytes")
		}
	}
	return in[:len(in)-pad], nil
}
