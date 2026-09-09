package totp

import (
	"runtime"
	"testing"
	"time"
)

func BenchmarkCodeAt(b *testing.B) {
	authenticator, err := New(rfc6238SHA1Secret, WithSkew(0))
	if err != nil {
		b.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	b.ReportAllocs()
	b.ResetTimer()

	var code string
	for i := 0; i < b.N; i++ {
		code = authenticator.CodeAt(at)
	}
	runtime.KeepAlive(code)
}

func BenchmarkAuthenticateAt(b *testing.B) {
	authenticator, err := New(rfc6238SHA1Secret, WithSkew(1))
	if err != nil {
		b.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	code := authenticator.CodeAt(at)
	b.ReportAllocs()
	b.ResetTimer()

	var valid bool
	for i := 0; i < b.N; i++ {
		valid = authenticator.AuthenticateAt(code, at)
	}
	runtime.KeepAlive(valid)
}
