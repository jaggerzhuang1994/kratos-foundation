package validation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIsValidEmail(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		want        error
	}{
		{"ordinary", "user@example.com", nil},
		{"plus tag", "a+b@example.com", nil},
		{"apostrophe", "o'hara@example.com", nil},
		{"atom punctuation", "a!#$%&'*+-/=?^_{}.b@example.com", nil},
		{"uppercase", "User@Example.COM", nil},
		{"subdomain and hyphen", "a@my-mail.example.com", nil},
		{"punycode domain", "a@xn--fsqu00a.xn--fiqs8s", nil},
		{"label limit", "a@" + strings.Repeat("b", 63) + ".com", nil},
		{"total limit", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), nil},
		{"empty", "", ErrEmailEmpty},
		{"leading local dot", ".a@example.com", ErrEmailInvalidFormat},
		{"trailing local dot", "a.@example.com", ErrEmailInvalidFormat},
		{"consecutive local dots", "a..b@example.com", ErrEmailInvalidFormat},
		{"leading domain hyphen", "a@-example.com", ErrEmailInvalidDomain},
		{"trailing domain hyphen", "a@example-.com", ErrEmailInvalidDomain},
		{"empty domain label", "a@example..com", ErrEmailInvalidFormat},
		{"trailing domain dot", "a@example.com.", ErrEmailInvalidFormat},
		{"leading domain dot", "a@.example.com", ErrEmailInvalidFormat},
		{"domain underscore", "a@ex_ample.com", ErrEmailInvalidDomain},
		{"missing local", "@example.com", ErrEmailInvalidFormat},
		{"missing domain", "user@", ErrEmailInvalidFormat},
		{"missing at", "user.example.com", ErrEmailInvalidFormat},
		{"multiple at", "user@@example.com", ErrEmailInvalidFormat},
		{"single label", "a@localhost", nil},
		{"short suffix", "a@example.c", nil},
		{"numeric suffix", "a@example.123", nil},
		{"numeric domain labels", "a@127.0.0.1", nil},
		{"IP literal", "a@[127.0.0.1]", ErrEmailInvalidDomain},
		{"unicode local", "中@example.com", nil},
		{"unicode domain", "a@例子.com", ErrEmailInvalidDomain},
		{"leading space", " a@example.com", ErrEmailNotPlainAddress},
		{"trailing space", "a@example.com ", ErrEmailNotPlainAddress},
		{"internal space", "a b@example.com", ErrEmailInvalidFormat},
		{"tab", "a\tb@example.com", ErrEmailInvalidFormat},
		{"CRLF", "a@example.com\r\n", ErrEmailInvalidFormat},
		{"control", "a\x00b@example.com", ErrEmailInvalidFormat},
		{"DEL", "a\x7fb@example.com", ErrEmailInvalidFormat},
		{"display name", "User <a@example.com>", ErrEmailNotPlainAddress},
		{"angle brackets", "<a@example.com>", ErrEmailNotPlainAddress},
		{"comment", "a@example.com(comment)", ErrEmailNotPlainAddress},
		{"address list", "a@example.com,b@example.com", ErrEmailInvalidFormat},
		{"quoted local", "\"user\"@example.com", ErrEmailNotPlainAddress},
		{"quoted at", "\"a@b\"@example.com", ErrEmailNotPlainAddress},
		{"local over 64 bytes", strings.Repeat("a", 65) + "@example.com", nil},
		{"label too long", "a@" + strings.Repeat("b", 64) + ".com", ErrEmailInvalidDomain},
		{"total too long", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62), ErrEmailTooLong},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := IsValidEmail(tt.input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if err != nil {
				if !errors.Is(fmt.Errorf("validate input: %w", err), tt.want) {
					t.Fatal("wrapped error lost its identity")
				}
				if tt.input != "" && strings.Contains(err.Error(), tt.input) {
					t.Fatal("error contains email input")
				}
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"plain", "  User@Example.COM\n", "user@example.com"},
		{"display name", " Alice <User@Example.COM> ", "user@example.com"},
		{"quoted display name", "\"Doe, Alice\" <User@Example.COM>", "user@example.com"},
		{"angle brackets", "<User@Example.COM>", "user@example.com"},
		{"comment", "User@Example.COM (Alice)", "user@example.com"},
		{"unicode display name", "小明 <User@Example.COM>", "user@example.com"},
		{"empty", " ", ""},
		{"malformed", " Alice <User@Example.COM ", "alice <user@example.com"},
		{"address list", "A@Example.COM, B@Example.COM", "a@example.com, b@example.com"},
		{"header field", "From: Alice <User@Example.COM>", "from: alice <user@example.com>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmail(tt.input); got != tt.want {
				t.Fatalf("NormalizeEmail() = %q, want %q", got, tt.want)
			}
		})
	}
	if err := IsValidEmail(NormalizeEmail("Alice <User@Example.COM>")); err != nil {
		t.Fatalf("normalized address rejected: %v", err)
	}
}
