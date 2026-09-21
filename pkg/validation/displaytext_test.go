package validation_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/validation"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/validation/displaytext"
)

func TestCheckDisplayText(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		options     validation.DisplayTextOptions
		want        error
	}{
		{"zero accepts empty", "", validation.DisplayTextOptions{}, nil},
		{"no preprocessing", " \x00\r\n ", validation.DisplayTextOptions{}, nil},
		{"invalid UTF8", "\xff", validation.DisplayTextOptions{}, validation.ErrDisplayTextInvalidUTF8},
		{"negative min", "a", validation.DisplayTextOptions{ByteLength: validation.LengthRange{Min: -1}}, validation.ErrDisplayTextInvalidConfig},
		{"negative max", "a", validation.DisplayTextOptions{UnicodeLength: validation.LengthRange{Max: -1}}, validation.ErrDisplayTextInvalidConfig},
		{"reversed", "a", validation.DisplayTextOptions{GraphemeLength: validation.LengthRange{Min: 2, Max: 1}}, validation.ErrDisplayTextInvalidConfig},
		{"config precedes encoding", "\xff", validation.DisplayTextOptions{GraphemeLength: validation.LengthRange{Min: -1}}, validation.ErrDisplayTextInvalidConfig},
		{"byte short", "a", validation.DisplayTextOptions{ByteLength: validation.LengthRange{Min: 2}}, validation.ErrDisplayTextByteLength},
		{"byte long", "中", validation.DisplayTextOptions{ByteLength: validation.LengthRange{Max: 2}}, validation.ErrDisplayTextByteLength},
		{"unicode short", "中", validation.DisplayTextOptions{UnicodeLength: validation.LengthRange{Min: 2}}, validation.ErrDisplayTextUnicodeLength},
		{"unicode long", "e\u0301", validation.DisplayTextOptions{UnicodeLength: validation.LengthRange{Max: 1}}, validation.ErrDisplayTextUnicodeLength},
		{"grapheme short", "👩‍💻", validation.DisplayTextOptions{GraphemeLength: validation.LengthRange{Min: 2}}, validation.ErrDisplayTextGraphemeLength},
		{"grapheme long", "ab", validation.DisplayTextOptions{GraphemeLength: validation.LengthRange{Max: 1}}, validation.ErrDisplayTextGraphemeLength},
		{"all exact bounds", "e\u0301", validation.DisplayTextOptions{ByteLength: validation.LengthRange{Min: 3, Max: 3}, UnicodeLength: validation.LengthRange{Min: 2, Max: 2}, GraphemeLength: validation.LengthRange{Min: 1, Max: 1}}, nil},
		{"unlimited max", "abc", validation.DisplayTextOptions{ByteLength: validation.LengthRange{Min: 1}}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validation.CheckDisplayText(tt.input, tt.options)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if err != nil && !errors.Is(fmt.Errorf("check: %w", err), tt.want) {
				t.Fatal("wrapped error lost identity")
			}
		})
	}
}

func ExampleCheckDisplayText() {
	result := displaytext.Preprocess(" 你好\r\n👩‍💻 ", displaytext.Options{TrimWhitespace: true, NewlinesToSpace: true})
	err := validation.CheckDisplayText(result.Output, validation.DisplayTextOptions{
		GraphemeLength: validation.LengthRange{Min: 1, Max: 20},
		UnicodeLength:  validation.LengthRange{Min: 1, Max: 255},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Output)
	// Output: 你好 👩‍💻
}
