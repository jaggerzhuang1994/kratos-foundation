package schnorr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
)

func TestSchnorrProofRoundTripSupportedCurves(t *testing.T) {
	t.Parallel()

	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		curve := curve
		t.Run(curve.Params().Name, func(t *testing.T) {
			t.Parallel()
			privateKey, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			x, y, response, err := SchnorrProof(privateKey, []byte("session-1"))
			if err != nil {
				t.Fatal(err)
			}
			valid, err := SchnorrProofVerify(x, y, response, &privateKey.PublicKey, []byte("session-1"))
			if err != nil {
				t.Fatal(err)
			}
			if !valid {
				t.Fatal("SchnorrProofVerify rejected a valid proof")
			}
		})
	}
}

func TestSchnorrProofBindsSessionAndResponse(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x, y, response, err := SchnorrProof(privateKey, []byte("session-1"))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := SchnorrProofVerify(x, y, response, &privateKey.PublicKey, []byte("session-2"))
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("SchnorrProofVerify accepted a proof for another session")
	}

	modified := new(big.Int).Add(response, big.NewInt(1))
	modified.Mod(modified, privateKey.Curve.Params().N)
	valid, err = SchnorrProofVerify(x, y, modified, &privateKey.PublicKey, []byte("session-1"))
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("SchnorrProofVerify accepted a modified response")
	}
}

func TestSchnorrRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = SchnorrProof(nil, []byte("session")); err == nil {
		t.Fatal("SchnorrProof accepted a nil private key")
	}
	if _, _, _, err = SchnorrProof(privateKey, nil); err == nil {
		t.Fatal("SchnorrProof accepted an empty session")
	}
	if _, err = SchnorrProofVerify(nil, nil, nil, &privateKey.PublicKey, []byte("session")); err == nil {
		t.Fatal("SchnorrProofVerify accepted an incomplete proof")
	}
	if _, err = SchnorrProofVerify(big.NewInt(1), big.NewInt(1), big.NewInt(1), &privateKey.PublicKey, []byte("session")); err == nil {
		t.Fatal("SchnorrProofVerify accepted a commitment outside the curve")
	}
	x, y := privateKey.Curve.ScalarBaseMult([]byte{1})
	if _, err = SchnorrProofVerify(x, y, privateKey.Curve.Params().N, &privateKey.PublicKey, []byte("session")); err == nil {
		t.Fatal("SchnorrProofVerify accepted a response outside the curve order")
	}
	if _, err = SchnorrProofVerify(x, y, big.NewInt(1), nil, []byte("session")); err == nil {
		t.Fatal("SchnorrProofVerify accepted a nil public key")
	}
}
