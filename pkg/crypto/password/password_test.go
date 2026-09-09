package password_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/password"
)

func testArgon2idParams() password.Argon2idParams {
	return password.Argon2idParams{
		MemoryKiB:   8,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		KeyLength:   16,
	}
}

func TestDefaultHashPasswordUsesDocumentedParameters(t *testing.T) {
	wantParams := password.Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 4,
		SaltLength:  16,
		KeyLength:   32,
	}
	if got := password.DefaultArgon2idParams(); got != wantParams {
		t.Fatalf("DefaultArgon2idParams() = %#v, want %#v", got, wantParams)
	}

	plain := []byte("correct horse battery staple")
	encoded, err := password.HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("HashPassword() = %q, want default Argon2id PHC costs", encoded)
	}
	assertPHCFieldLength(t, encoded, 4, 16)
	assertPHCFieldLength(t, encoded, 5, 32)
	matched, err := password.VerifyPassword(plain, encoded)
	if err != nil || !matched {
		t.Fatalf("VerifyPassword(correct) = %v, err = %v", matched, err)
	}
}

func TestHashPasswordWithParamsRoundTripsAndRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	plain := []byte("correct horse battery staple")
	encoded, err := password.HashPasswordWithParams(plain, testArgon2idParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("HashPasswordWithParams() = %q, want requested Argon2id PHC costs", encoded)
	}
	assertPHCFieldLength(t, encoded, 4, 8)
	assertPHCFieldLength(t, encoded, 5, 16)

	matched, err := password.VerifyPassword(plain, encoded)
	if err != nil || !matched {
		t.Fatalf("VerifyPassword(correct) = %v, err = %v", matched, err)
	}
	matched, err = password.VerifyPassword([]byte("wrong password"), encoded)
	if err != nil || matched {
		t.Fatalf("VerifyPassword(wrong) = %v, err = %v", matched, err)
	}
}

func TestHashPasswordAcceptsEmptyPassword(t *testing.T) {
	t.Parallel()

	encoded, err := password.HashPasswordWithParams(nil, testArgon2idParams())
	if err != nil {
		t.Fatal(err)
	}
	matched, err := password.VerifyPassword(nil, encoded)
	if err != nil || !matched {
		t.Fatalf("VerifyPassword(empty) = %v, err = %v", matched, err)
	}
	matched, err = password.VerifyPassword([]byte("non-empty"), encoded)
	if err != nil || matched {
		t.Fatalf("VerifyPassword(non-empty) = %v, err = %v", matched, err)
	}
}

func TestNeedsRehashComparesAllParametersAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	params := testArgon2idParams()
	encoded, err := password.HashPasswordWithParams([]byte("password"), params)
	if err != nil {
		t.Fatal(err)
	}
	needsRehash, err := password.NeedsRehash(encoded, params)
	if err != nil || needsRehash {
		t.Fatalf("NeedsRehash(same) = %v, err = %v", needsRehash, err)
	}
	stronger := params
	stronger.MemoryKiB = 16
	needsRehash, err = password.NeedsRehash(encoded, stronger)
	if err != nil || !needsRehash {
		t.Fatalf("NeedsRehash(changed) = %v, err = %v", needsRehash, err)
	}
	invalid := params
	invalid.Iterations = 0
	if _, err := password.NeedsRehash(encoded, invalid); err == nil {
		t.Fatal("NeedsRehash accepted invalid desired parameters")
	}
	if _, err := password.NeedsRehash("not-a-phc", params); err == nil {
		t.Fatal("NeedsRehash accepted malformed encoded hash")
	}
}

func TestArgon2idParamsRejectUnsafeOrExcessiveValues(t *testing.T) {
	t.Parallel()

	valid := testArgon2idParams()
	tests := map[string]password.Argon2idParams{}
	mutate := func(name string, change func(*password.Argon2idParams)) {
		params := valid
		change(&params)
		tests[name] = params
	}
	mutate("zero iterations", func(params *password.Argon2idParams) { params.Iterations = 0 })
	mutate("excessive iterations", func(params *password.Argon2idParams) { params.Iterations = 21 })
	mutate("zero parallelism", func(params *password.Argon2idParams) { params.Parallelism = 0 })
	mutate("excessive parallelism", func(params *password.Argon2idParams) { params.Parallelism = 17 })
	mutate("insufficient memory", func(params *password.Argon2idParams) { params.MemoryKiB = 7 })
	mutate("excessive memory", func(params *password.Argon2idParams) { params.MemoryKiB = 256*1024 + 1 })
	mutate("short salt", func(params *password.Argon2idParams) { params.SaltLength = 7 })
	mutate("long salt", func(params *password.Argon2idParams) { params.SaltLength = 65 })
	mutate("short key", func(params *password.Argon2idParams) { params.KeyLength = 15 })
	mutate("long key", func(params *password.Argon2idParams) { params.KeyLength = 65 })

	for name, params := range tests {
		params := params
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := password.HashPasswordWithParams([]byte("password"), params); err == nil {
				t.Fatal("HashPasswordWithParams accepted invalid parameters")
			}
		})
	}
}

func TestVerifyPasswordRejectsMalformedPHC(t *testing.T) {
	t.Parallel()

	malformed := []string{
		"",
		"$argon2i$v=19$m=8,t=1,p=1$MTIzNDU2Nzg$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=16$m=8,t=1,p=1$MTIzNDU2Nzg$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$t=1,m=8,p=1$MTIzNDU2Nzg$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=1$%%%$AAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=1$MTIzNDU2Nzg$%%%",
	}
	for _, encoded := range malformed {
		if _, err := password.VerifyPassword([]byte("password"), encoded); err == nil {
			t.Errorf("VerifyPassword accepted malformed PHC %q", encoded)
		}
	}
}

func TestVerifyPasswordRejectsOversizedPHCFieldsBeforeDecode(t *testing.T) {
	t.Parallel()

	validSalt := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	validHash := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	oversized := base64.RawStdEncoding.EncodeToString(make([]byte, 65))

	tests := map[string]string{
		"salt": "$argon2id$v=19$m=8,t=1,p=1$" + oversized + "$" + validHash,
		"hash": "$argon2id$v=19$m=8,t=1,p=1$" + validSalt + "$" + oversized,
	}
	for name, encoded := range tests {
		encoded := encoded
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := password.VerifyPassword([]byte("password"), encoded)
			if err == nil || !strings.Contains(err.Error(), "PHC field exceeds maximum encoded length") {
				t.Fatalf("VerifyPassword() error = %v, want pre-decode length rejection", err)
			}
		})
	}
}

func assertPHCFieldLength(t testing.TB, encoded string, field, want int) {
	t.Helper()
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC fields = %d, want 6: %q", len(parts), encoded)
	}
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(parts[field])
	if err != nil {
		t.Fatalf("decode PHC field %d: %v", field, err)
	}
	if len(decoded) != want {
		t.Fatalf("PHC field %d length = %d, want %d", field, len(decoded), want)
	}
}
