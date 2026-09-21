package validation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIsValidDigits(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		input                string
		minLength, maxLength int
		want                 error
	}{
		{"lower bound", "0123", 4, 8, nil},
		{"upper bound", "12345678", 4, 8, nil},
		{"fixed length", "000000", 6, 6, nil},
		{"single zero", "0", 1, 1, nil},
		{"short", "123", 4, 8, ErrDigitsTooShort},
		{"long", "123456789", 4, 8, ErrDigitsTooLong},
		{"empty", "", 1, 8, ErrDigitsTooShort},
		{"letters", "12a3", 4, 8, ErrDigitsInvalidCharacter},
		{"leading space", " 1234", 4, 8, ErrDigitsInvalidCharacter},
		{"internal space", "12 34", 4, 8, ErrDigitsInvalidCharacter},
		{"newline", "1234\n", 4, 8, ErrDigitsInvalidCharacter},
		{"positive sign", "+1234", 4, 8, ErrDigitsInvalidCharacter},
		{"negative sign", "-1234", 4, 8, ErrDigitsInvalidCharacter},
		{"decimal", "12.34", 4, 8, ErrDigitsInvalidCharacter},
		{"full width", "１２３４", 1, 20, ErrDigitsInvalidCharacter},
		{"Arabic digits", "١٢٣٤", 1, 20, ErrDigitsInvalidCharacter},
		{"zero minimum", "1234", 0, 8, ErrDigitsInvalidConfig},
		{"negative minimum", "1234", -1, 8, ErrDigitsInvalidConfig},
		{"reversed range", "1234", 8, 4, ErrDigitsInvalidConfig},
		{"zero maximum", "1", 1, 0, ErrDigitsInvalidConfig},
		{"large numeric string", strings.Repeat("9", 100), 1, 100, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := IsValidDigits(tt.input, tt.minLength, tt.maxLength)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if err != nil && !errors.Is(fmt.Errorf("validate input: %w", err), tt.want) {
				t.Fatal("wrapped error lost its identity")
			}
		})
	}
}
