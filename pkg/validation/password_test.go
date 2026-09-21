package validation

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsValidPassword(t *testing.T) {
	for _, tt := range []struct {
		name, password                 string
		minLength, maxLength, minTypes int
		wantErr                        error
	}{
		{"lower only", "abcdef", 6, 25, 1, nil},
		{"upper only", "ABCDEF", 6, 25, 1, nil},
		{"digits only", "123456", 6, 25, 1, nil},
		{"special only", "!@#$%^", 6, 25, 1, nil},
		{"one type insufficient", "abcdef", 6, 25, 2, ErrPasswordInsufficientTypes},
		{"two types", "Abcdef", 6, 25, 2, nil},
		{"two types insufficient", "Abcdef", 6, 25, 3, ErrPasswordInsufficientTypes},
		{"three types", "Abc123", 6, 25, 3, nil},
		{"three types insufficient", "Abc123", 6, 25, 4, ErrPasswordInsufficientTypes},
		{"four types", "Abc12!", 6, 25, 4, nil},
		{"space is special", "Abc12 ", 6, 25, 4, nil},
		{"space retained", " abc ", 5, 5, 2, nil},
		{"ASCII endpoints", " ~", 2, 2, 1, nil},
		{"empty", "", 1, 25, 1, ErrPasswordTooShort},
		{"too short", "Ab1!", 5, 10, 4, ErrPasswordTooShort},
		{"too long", "Ab123!", 1, 5, 4, ErrPasswordTooLong},
		{"exact length", "Ab1!", 4, 4, 4, nil},
		{"control below space", "Ab1!\x1f", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"DEL", "Ab1!\x7f", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"tab", "Ab1!\t", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"newline", "Ab1!\n", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"Unicode", "Ab1!中", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"invalid UTF8", "Ab1!\xff", 1, 25, 4, ErrPasswordInvalidCharacter},
		{"zero minimum", "Ab1!", 0, 25, 4, ErrPasswordInvalidConfig},
		{"negative minimum", "Ab1!", -1, 25, 4, ErrPasswordInvalidConfig},
		{"reversed range", "Ab1!", 5, 4, 4, ErrPasswordInvalidConfig},
		{"zero types", "Ab1!", 1, 25, 0, ErrPasswordInvalidConfig},
		{"negative types", "Ab1!", 1, 25, -1, ErrPasswordInvalidConfig},
		{"too many types", "Ab1!", 1, 25, 5, ErrPasswordInvalidConfig},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := IsValidPassword(tt.password, tt.minLength, tt.maxLength, tt.minTypes)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(fmt.Errorf("validate input: %w", err), tt.wantErr) {
				t.Fatal("wrapped error lost its identity")
			}

		})
	}
}
