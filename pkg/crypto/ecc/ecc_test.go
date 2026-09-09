package ecc

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

func TestECIESRoundTripSupportedCurves(t *testing.T) {
	t.Parallel()

	curves := []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()}
	for _, curve := range curves {
		curve := curve
		t.Run(curve.Params().Name, func(t *testing.T) {
			t.Parallel()
			privateKey, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := Encrypt("ECIES payload", &privateKey.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := Decrypt(ciphertext, privateKey)
			if err != nil {
				t.Fatal(err)
			}
			if string(plain) != "ECIES payload" {
				t.Fatalf("Decrypt() = %q", plain)
			}
		})
	}
}

func TestDecryptRejectsNegatedEphemeralPoint(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := Encrypt("confidential", publicKey)
	if err != nil {
		t.Fatal(err)
	}

	publicLength := 1 + 2*((publicKey.Curve.Params().BitSize+7)/8)
	x, y := elliptic.Unmarshal(publicKey.Curve, ciphertext[:publicLength])
	if x == nil || y == nil {
		t.Fatal("Encrypt produced an invalid ephemeral public key")
	}
	negatedY := new(big.Int).Sub(publicKey.Curve.Params().P, y)
	copy(ciphertext[:publicLength], elliptic.Marshal(publicKey.Curve, x, negatedY))

	if _, err = Decrypt(ciphertext, privateKey); !errors.Is(err, errInvalidCiphertext) {
		t.Fatalf("Decrypt() error = %v, want %v", err, errInvalidCiphertext)
	}
}

func TestECIESBase64AndPublicHexRoundTrip(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncryptToBase64String("base64 payload", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptFromBase64String(encoded, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "base64 payload" {
		t.Fatalf("DecryptFromBase64String() = %q", plain)
	}

	coordinateSize := (publicKey.Curve.Params().BitSize + 7) / 8
	coordinates := make([]byte, coordinateSize*2)
	publicKey.X.FillBytes(coordinates[:coordinateSize])
	publicKey.Y.FillBytes(coordinates[coordinateSize:])
	encoded, err = EncryptToBase64StringByPubHex("hex payload", hex.EncodeToString(coordinates))
	if err != nil {
		t.Fatal(err)
	}
	plain, err = DecryptFromBase64String(encoded, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "hex payload" {
		t.Fatalf("hex-key round trip = %q", plain)
	}
}

func TestECKeyPEMRoundTrip(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	privatePEM, err := EncodePrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	parsedPrivate, err := ParsePrivateKey(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	if parsedPrivate.D.Cmp(privateKey.D) != 0 {
		t.Fatal("parsed private key scalar differs")
	}
	publicPEM, err := EncodePublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	parsedPublic, err := ParsePublicKey(publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	if parsedPublic.X.Cmp(publicKey.X) != 0 || parsedPublic.Y.Cmp(publicKey.Y) != 0 {
		t.Fatal("parsed public key point differs")
	}
}

func TestECIESRejectsInvalidInputsAndTampering(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Encrypt("payload", nil); err == nil {
		t.Fatal("Encrypt accepted a nil public key")
	}
	if _, err = Encrypt("payload", &ecdsa.PublicKey{Curve: elliptic.P256(), X: big.NewInt(1), Y: big.NewInt(1)}); err == nil {
		t.Fatal("Encrypt accepted a point outside the curve")
	}
	if _, err = Decrypt(nil, privateKey); !errors.Is(err, errInvalidCiphertext) {
		t.Fatalf("Decrypt(nil) error = %v", err)
	}
	if _, err = DecryptFromBase64String("%%%", privateKey); err == nil {
		t.Fatal("DecryptFromBase64String accepted invalid Base64")
	}
	if _, err = EncryptToBase64StringByPubHex("payload", "00"); err == nil {
		t.Fatal("EncryptToBase64StringByPubHex accepted the wrong key length")
	}

	ciphertext, err := Encrypt("authenticated", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 1
	if _, err = Decrypt(tampered, privateKey); !errors.Is(err, errInvalidCiphertext) {
		t.Fatalf("Decrypt(tampered) error = %v, want %v", err, errInvalidCiphertext)
	}
}

func TestECKeyParsersRejectInvalidPEM(t *testing.T) {
	t.Parallel()

	if _, err := ParsePrivateKey("not PEM"); err == nil {
		t.Fatal("ParsePrivateKey accepted invalid PEM")
	}
	if _, err := ParsePublicKey("not PEM"); err == nil {
		t.Fatal("ParsePublicKey accepted invalid PEM")
	}
	if _, err := EncodePrivateKey(nil); err == nil {
		t.Fatal("EncodePrivateKey accepted nil")
	}
	if _, err := EncodePublicKey(nil); err == nil {
		t.Fatal("EncodePublicKey accepted nil")
	}
}
