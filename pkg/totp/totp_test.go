package totp

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

const rfc6238SHA1Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestRFC6238SHA1Vectors(t *testing.T) {
	t.Parallel()

	authenticator, err := New(rfc6238SHA1Secret, WithDigits(8), WithSkew(0))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		unix int64
		want string
	}{
		{unix: 59, want: "94287082"},
		{unix: 1111111109, want: "07081804"},
		{unix: 1111111111, want: "14050471"},
		{unix: 1234567890, want: "89005924"},
		{unix: 2000000000, want: "69279037"},
		{unix: 20000000000, want: "65353130"},
	}
	for _, test := range tests {
		at := time.Unix(test.unix, 0)
		if got := authenticator.CodeAt(at); got != test.want {
			t.Errorf("CodeAt(%d) = %q, want %q", test.unix, got, test.want)
		}
		if !authenticator.AuthenticateAt(test.want, at) {
			t.Errorf("AuthenticateAt(%q, %d) = false, want true", test.want, test.unix)
		}
	}
}

func TestWithSkewRejectsUnsafeWindow(t *testing.T) {
	t.Parallel()

	if _, err := New(rfc6238SHA1Secret, WithSkew(3)); err == nil {
		t.Fatal("New accepted a TOTP skew larger than two steps")
	}
}

func TestAuthenticateUsesConfiguredSkewAndRejectsMalformedCodes(t *testing.T) {
	t.Parallel()

	authenticator, err := New(rfc6238SHA1Secret, WithSkew(1))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(90, 0)
	previousCode := authenticator.CodeAt(time.Unix(60, 0))
	if !authenticator.AuthenticateAt(previousCode, at) {
		t.Fatal("AuthenticateAt rejected a code from the configured previous step")
	}
	for _, code := range []string{"", "12345", "1234567", "12a456"} {
		if authenticator.AuthenticateAt(code, at) {
			t.Errorf("AuthenticateAt(%q) = true, want false", code)
		}
	}
}

func TestCurrentAndAuthenticateUseWallClock(t *testing.T) {
	t.Parallel()

	authenticator, err := New(rfc6238SHA1Secret)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	current := authenticator.Current()
	after := time.Now()
	if current != authenticator.CodeAt(before) && current != authenticator.CodeAt(after) {
		t.Fatalf("Current() = %q, want code for a time within [%s, %s]", current, before, after)
	}
	if !authenticator.Authenticate(current) {
		t.Fatal("Authenticate rejected the current code")
	}
}

func TestGenerateSecretReturnsValidUnpaddedBase32(t *testing.T) {
	t.Parallel()

	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != defaultSecretBytes {
		t.Fatalf("decoded secret has %d bytes, want %d", len(decoded), defaultSecretBytes)
	}
	if strings.Contains(secret, "=") {
		t.Fatalf("GenerateSecret() returned padded secret %q", secret)
	}
}

func TestNewNormalizesSecretAndValidatesOptions(t *testing.T) {
	t.Parallel()

	authenticator, err := New("  " + strings.ToLower(rfc6238SHA1Secret) + "====  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := authenticator.Secret(); got != rfc6238SHA1Secret {
		t.Fatalf("Secret() = %q, want %q", got, rfc6238SHA1Secret)
	}

	invalidOptions := []Option{
		nil,
		WithPeriod(500 * time.Millisecond),
		WithDigits(7),
		WithSkew(-1),
	}
	for _, option := range invalidOptions {
		if _, err = New(rfc6238SHA1Secret, option); err == nil {
			t.Errorf("New accepted invalid option %#v", option)
		}
	}
	for _, secret := range []string{"", "not-base32", "GEZDGNBV"} {
		if _, err = New(secret); err == nil {
			t.Errorf("New accepted invalid secret %q", secret)
		}
	}
}

func TestProvisionURIWithIssuer(t *testing.T) {
	t.Parallel()

	authenticator, err := New(rfc6238SHA1Secret, WithDigits(8), WithPeriod(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := authenticator.ProvisionURIWithIssuer("alice@example.com", "Example Inc")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" || parsed.Path != "/Example Inc:alice@example.com" {
		t.Fatalf("ProvisionURIWithIssuer() identity = %s://%s%s", parsed.Scheme, parsed.Host, parsed.Path)
	}
	query := parsed.Query()
	if query.Get("secret") != rfc6238SHA1Secret || query.Get("issuer") != "Example Inc" ||
		query.Get("digits") != "8" || query.Get("period") != "60" {
		t.Fatalf("ProvisionURIWithIssuer() query = %v", query)
	}
	if _, err = authenticator.ProvisionURI("  "); err == nil {
		t.Fatal("ProvisionURI accepted an empty account")
	}
}
