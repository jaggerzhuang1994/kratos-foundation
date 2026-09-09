package schnorr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"runtime"
	"testing"
)

func BenchmarkSchnorrProofP256(b *testing.B) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	session := []byte("benchmark-session")
	b.ReportAllocs()
	b.ResetTimer()

	var x, y, response *big.Int
	for i := 0; i < b.N; i++ {
		x, y, response, err = SchnorrProof(privateKey, session)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(x)
	runtime.KeepAlive(y)
	runtime.KeepAlive(response)
}

func BenchmarkSchnorrVerifyP256(b *testing.B) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	session := []byte("benchmark-session")
	x, y, response, err := SchnorrProof(privateKey, session)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	var valid bool
	for i := 0; i < b.N; i++ {
		valid, err = SchnorrProofVerify(x, y, response, &privateKey.PublicKey, session)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(valid)
}
