package rsa

import (
	"bytes"
	"testing"
)

func TestRSARoundTripSingleAndMultipleBlocks(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey(1024)
	if err != nil {
		t.Fatal(err)
	}
	maximumBlock := publicKey.Size() - 11
	plaintexts := [][]byte{
		nil,
		[]byte("short payload"),
		bytes.Repeat([]byte("x"), maximumBlock),
		bytes.Repeat([]byte("multi-block payload"), 30),
	}
	for _, plaintext := range plaintexts {
		encrypted, err := Encrypt(plaintext, publicKey)
		if err != nil {
			t.Fatalf("Encrypt(len=%d): %v", len(plaintext), err)
		}
		decrypted, err := Decrypt(encrypted, privateKey)
		if err != nil {
			t.Fatalf("Decrypt(len=%d): %v", len(plaintext), err)
		}
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatalf("round trip len=%d = %x, want %x", len(plaintext), decrypted, plaintext)
		}
	}
}

func TestRSABase64RoundTrip(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey(1024)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncryptToBase64String([]byte("base64 payload"), publicKey)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := DecryptFromBase64String(encoded, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != "base64 payload" {
		t.Fatalf("DecryptFromBase64String() = %q", decrypted)
	}
}

func TestRSAKeyPEMRoundTrip(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey(1024)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := EncodePrivateKey(privateKey)
	parsedPrivate, err := ParsePrivateKey(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	if parsedPrivate.N.Cmp(privateKey.N) != 0 || parsedPrivate.D.Cmp(privateKey.D) != 0 {
		t.Fatal("parsed RSA private key differs")
	}
	publicPEM := EncodePublicKey(publicKey)
	parsedPublic, err := ParsePublicKey(publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	if parsedPublic.N.Cmp(publicKey.N) != 0 || parsedPublic.E != publicKey.E {
		t.Fatal("parsed RSA public key differs")
	}
}

func TestRSARejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey(1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Encrypt([]byte("payload"), nil); err == nil {
		t.Fatal("Encrypt accepted a nil public key")
	}
	if _, err = Decrypt([]byte("payload"), nil); err == nil {
		t.Fatal("Decrypt accepted a nil private key")
	}
	if _, err = Decrypt(make([]byte, publicKey.Size()-1), privateKey); err == nil {
		t.Fatal("Decrypt accepted a partial RSA block")
	}
	if _, err = DecryptFromBase64String("%%%", privateKey); err == nil {
		t.Fatal("DecryptFromBase64String accepted invalid Base64")
	}
	if got := EncodePrivateKey(nil); got != "" {
		t.Fatalf("EncodePrivateKey(nil) = %q", got)
	}
	if got := EncodePublicKey(nil); got != "" {
		t.Fatalf("EncodePublicKey(nil) = %q", got)
	}
	if _, err = ParsePrivateKey("not PEM"); err == nil {
		t.Fatal("ParsePrivateKey accepted invalid PEM")
	}
	if _, err = ParsePublicKey("not PEM"); err == nil {
		t.Fatal("ParsePublicKey accepted invalid PEM")
	}
}
