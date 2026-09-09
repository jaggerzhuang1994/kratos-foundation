package password

import (
	"runtime"
	"testing"
)

func BenchmarkHashPasswordDefault(b *testing.B) {
	password := []byte("correct horse battery staple")
	b.ReportAllocs()
	b.ResetTimer()

	var encoded string
	for i := 0; i < b.N; i++ {
		var err error
		encoded, err = HashPassword(password)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(encoded)
}

func BenchmarkVerifyPasswordDefault(b *testing.B) {
	password := []byte("correct horse battery staple")
	encoded, err := HashPassword(password)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	var matched bool
	for i := 0; i < b.N; i++ {
		matched, err = VerifyPassword(password, encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(matched)
}
