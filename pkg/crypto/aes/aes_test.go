package aes

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCBCRoundTripSupportedKeySizes(t *testing.T) {
	t.Parallel()

	plaintexts := [][]byte{nil, {}, []byte("short"), bytes.Repeat([]byte("x"), 16), bytes.Repeat([]byte("payload"), 100)}
	for _, keySize := range []int{16, 24, 32} {
		key := bytes.Repeat([]byte{byte(keySize)}, keySize)
		for _, plaintext := range plaintexts {
			encrypted, err := (CBC{}).Encrypt(plaintext, key)
			if err != nil {
				t.Fatalf("Encrypt(keySize=%d, len=%d): %v", keySize, len(plaintext), err)
			}
			decrypted, err := (CBC{}).Decrypt(encrypted, key)
			if err != nil {
				t.Fatalf("Decrypt(keySize=%d, len=%d): %v", keySize, len(plaintext), err)
			}
			if !bytes.Equal(decrypted, plaintext) {
				t.Fatalf("round trip(keySize=%d) = %x, want %x", keySize, decrypted, plaintext)
			}
		}
	}
}

func TestCBCUsesFreshIV(t *testing.T) {
	t.Parallel()

	cipher := CBC{}
	key := bytes.Repeat([]byte{1}, 32)
	first, err := cipher.Encrypt([]byte("same plaintext"), key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt([]byte("same plaintext"), key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("Encrypt produced identical ciphertexts for the same plaintext and key")
	}
}

func TestCBCStringRoundTrip(t *testing.T) {
	t.Parallel()

	cipher := CBC{}
	key := "0123456789abcdef0123456789abcdef"
	encrypted, err := cipher.EncryptString("你好, kratos", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = base64.StdEncoding.DecodeString(encrypted); err != nil {
		t.Fatalf("EncryptString returned invalid Base64: %v", err)
	}
	decrypted, err := cipher.DecryptString(encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "你好, kratos" {
		t.Fatalf("DecryptString() = %q", decrypted)
	}
}

func TestCBCRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	cipher := CBC{}
	if _, err := cipher.Encrypt([]byte("data"), []byte("short")); err == nil {
		t.Fatal("Encrypt accepted an invalid AES key")
	}
	if _, err := cipher.Decrypt(make([]byte, 16), make([]byte, 16)); err == nil {
		t.Fatal("Decrypt accepted ciphertext without a data block")
	}
	if _, err := cipher.Decrypt(make([]byte, 33), make([]byte, 16)); err == nil {
		t.Fatal("Decrypt accepted a partial ciphertext block")
	}
	if _, err := cipher.DecryptString("%%%", "0123456789abcdef"); err == nil {
		t.Fatal("DecryptString accepted invalid Base64")
	}
}

func TestPKCS7PaddingRoundTripAndValidation(t *testing.T) {
	t.Parallel()

	for length := 0; length <= 32; length++ {
		plain := bytes.Repeat([]byte{byte(length)}, length)
		padded := pkcs7Pad(plain, 16)
		unpadded, err := pkcs7Unpad(padded, 16)
		if err != nil {
			t.Fatalf("pkcs7Unpad(len=%d): %v", length, err)
		}
		if !bytes.Equal(unpadded, plain) {
			t.Fatalf("pkcs7 round trip len=%d = %x, want %x", length, unpadded, plain)
		}
	}
	for _, invalid := range [][]byte{nil, {0}, bytes.Repeat([]byte{0}, 16), append(bytes.Repeat([]byte{0}, 14), 3, 2)} {
		if _, err := pkcs7Unpad(invalid, 16); err == nil {
			t.Errorf("pkcs7Unpad accepted %x", invalid)
		}
	}
}
