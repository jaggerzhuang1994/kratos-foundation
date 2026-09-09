package rsa

import (
	"runtime"
	"testing"
)

func BenchmarkRSAEncrypt2048(b *testing.B) {
	_, publicKey, err := GenerateKey(2048)
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte("benchmark payload")
	b.ReportAllocs()
	b.ResetTimer()

	var ciphertext []byte
	for i := 0; i < b.N; i++ {
		ciphertext, err = Encrypt(payload, publicKey)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(ciphertext)
}

func BenchmarkRSADecrypt2048(b *testing.B) {
	privateKey, publicKey, err := GenerateKey(2048)
	if err != nil {
		b.Fatal(err)
	}
	ciphertext, err := Encrypt([]byte("benchmark payload"), publicKey)
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
