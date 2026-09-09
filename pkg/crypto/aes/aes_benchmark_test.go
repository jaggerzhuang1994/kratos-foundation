package aes

import (
	"bytes"
	"runtime"
	"testing"
)

func BenchmarkCBCEncrypt1KiB(b *testing.B) {
	cipher := CBC{}
	key := bytes.Repeat([]byte{1}, 32)
	plain := bytes.Repeat([]byte("x"), 1<<10)
	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()

	var encrypted []byte
	for i := 0; i < b.N; i++ {
		var err error
		encrypted, err = cipher.Encrypt(plain, key)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(encrypted)
}

func BenchmarkCBCDecrypt1KiB(b *testing.B) {
	cipher := CBC{}
	key := bytes.Repeat([]byte{1}, 32)
	plain := bytes.Repeat([]byte("x"), 1<<10)
	encrypted, err := cipher.Encrypt(plain, key)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()

	var decrypted []byte
	for i := 0; i < b.N; i++ {
		decrypted, err = cipher.Decrypt(encrypted, key)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(decrypted)
}
