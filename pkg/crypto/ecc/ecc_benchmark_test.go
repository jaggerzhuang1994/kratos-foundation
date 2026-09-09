package ecc

import (
	"runtime"
	"testing"
)

func BenchmarkECIESEncryptP256(b *testing.B) {
	_, publicKey, err := GenerateKey()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	var ciphertext []byte
	for i := 0; i < b.N; i++ {
		ciphertext, err = Encrypt("benchmark payload", publicKey)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(ciphertext)
}

func BenchmarkECIESDecryptP256(b *testing.B) {
	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		b.Fatal(err)
	}
	ciphertext, err := Encrypt("benchmark payload", publicKey)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	var plain []byte
	for i := 0; i < b.N; i++ {
		plain, err = Decrypt(ciphertext, privateKey)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(plain)
}
