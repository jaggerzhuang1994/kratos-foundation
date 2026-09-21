package displaytext_test

import (
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/validation/displaytext"
	"github.com/rivo/uniseg"
)

func TestPreprocess(t *testing.T) {
	all := displaytext.Options{CollapseSpaces: true, NewlinesToSpace: true, WhitespaceToSpace: true, TrimWhitespace: true}
	tests := []struct {
		name, input, want string
		options           displaytext.Options
	}{
		{"empty", "", "", all},
		{"mixed", " \t你好\r\n\r\n👩‍💻  e\u0301\u00A0 ", "你好 👩‍💻 e\u0301", all},
		{"defaults", " \tA\r\n  B\u00A0 ", " \tA\r\n  B\u00A0 ", displaytext.Options{}},
		{"newlines only", "A\t\r\r\n\nB", "A\t B", displaytext.Options{NewlinesToSpace: true}},
		{"whitespace only", "A\t\t\r\nB\u00A0", "A  \r\nB ", displaytext.Options{WhitespaceToSpace: true}},
		{"collapse only", "A  \t  B", "A \t B", displaytext.Options{CollapseSpaces: true}},
		{"controls", "A\x00\u200BB", "AB", all},
		{"emoji", "❤️👩‍💻🇨🇳", "❤️👩‍💻🇨🇳", all},
		{"orphan selector", "\uFE0Fa", "a", all},
		{"trim selector", " \uFE0Fa", " \uFE0Fa", all},
		{"mixed cluster retained", "a\u00A0\u0301b", "a\u00A0\u0301b", all},
		{"space selector preserved", "a  \uFE0Fb", "a  \uFE0Fb", all},
		{"attached zwj", "a\u200Db", "a\u200Db", all},
		{"invalid utf8", "a\xff\xfeb", "a\uFFFDb", all},
		{"unicode separators", "a\u0085\u2028\u2029b", "a b", all},
		{"copied BOM and zero width edges", "\uFEFF\u200B 标题 \u200B\uFEFF", "标题", all},
		{"word joiner and soft hyphen", "\u2060co\u00ADoperate\u2060", "cooperate", all},
		{"bidi controls", "\u202A标题\u202C\u2066\u2069", "标题", all},
		{"only invisible", "\uFEFF\u200B\u2060\x00", "", all},
		{"edge and interior invisible", "\u200Bfoo\u200Bbar\u200B", "foobar", all},
		{"delete exposes adjacent spaces", "a \u200B b", "a b", all},
		{"clipboard list marker", "\u00A0• 标题★\u00A0", "• 标题★", all},
		{"clipboard quote", "\uFEFF“不要删除引号”\u200B", "“不要删除引号”", all},
		{"mixed line endings", "a\r\nb\rc\nd", "a b c d", all},
		{"multiline preserved", "  第一行\r\n	第二行  ", "第一行\r\n	第二行", displaytext.Options{TrimWhitespace: true}},
		{"space runs preserved", "a   b", "a   b", displaytext.Options{TrimWhitespace: true}},
		{"NBSP preserved", "a\u00A0b", "a\u00A0b", displaytext.Options{TrimWhitespace: true}},
		{"trailing cluster selector", "a \uFE0F", "a \uFE0F", all},
		{"orphan combining mark", "\u0301a", "\u0301a", all},
		{"invalid runs separated", "\xffA\xfe\xffB", "\uFFFDA\uFFFDB", all},
		{"genuine replacement character", "a\uFFFDb", "a\uFFFDb", all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displaytext.Preprocess(tt.input, tt.options)
			if got.Original != tt.input || got.Output != tt.want {
				t.Fatalf("got %#v, want output %q", got, tt.want)
			}
			if got.ByteLength != len(tt.want) || got.UnicodeLength != utf8.RuneCountInString(tt.want) || got.GraphemeLength != uniseg.GraphemeClusterCount(tt.want) {
				t.Fatalf("wrong lengths: %#v", got)
			}
			if next := displaytext.Preprocess(got.Output, tt.options); next.Output != got.Output {
				t.Fatalf("not idempotent: %#v -> %#v", got, next)
			}
		})
	}
}

func TestOptionCombinations(t *testing.T) {
	for bits := 0; bits < 16; bits++ {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			options := displaytext.Options{CollapseSpaces: bits&1 != 0, NewlinesToSpace: bits&2 != 0, WhitespaceToSpace: bits&4 != 0, TrimWhitespace: bits&8 != 0}
			a, b, c := "  ", "\r\n\n", "\t\u00A0"
			if options.CollapseSpaces {
				a = " "
			}
			if options.NewlinesToSpace {
				b = " "
			}
			if options.WhitespaceToSpace {
				c = "  "
				if options.CollapseSpaces {
					c = " "
				}
			}
			want := "A" + a + "B" + b + "C" + c + "D"
			if !options.TrimWhitespace {
				want = a + want + a
			}
			if got := displaytext.Preprocess("  A  B\r\n\nC\t\u00A0D  ", options); got.Output != want {
				t.Fatalf("got %q, want %q", got.Output, want)
			}
		})
	}
}

func ExamplePreprocess() {
	r := displaytext.Preprocess(" \t你好\r\n👩‍💻  e\u0301 ", displaytext.Options{
		CollapseSpaces: true, NewlinesToSpace: true, WhitespaceToSpace: true, TrimWhitespace: true,
	})
	fmt.Printf("%s\n%d %d %d\n", r.Output, r.ByteLength, r.UnicodeLength, r.GraphemeLength)
	// Output:
	// 你好 👩‍💻 é
	// 22 9 6
}

func FuzzPreprocess(f *testing.F) {
	for _, s := range []string{"", " \uFE0Fa", "👩‍💻\x00❤️", "a\r\n\t\u00A0", "\xff"} {
		for bits := uint8(0); bits < 16; bits++ {
			f.Add(s, bits)
		}
	}
	f.Fuzz(func(t *testing.T, s string, bits uint8) {
		options := displaytext.Options{CollapseSpaces: bits&1 != 0, NewlinesToSpace: bits&2 != 0, WhitespaceToSpace: bits&4 != 0, TrimWhitespace: bits&8 != 0}
		r := displaytext.Preprocess(s, options)
		if r.Original != s || !utf8.ValidString(r.Output) {
			t.Fatal("original or UTF-8 invariant")
		}
		if r.ByteLength != len(r.Output) || r.UnicodeLength != utf8.RuneCountInString(r.Output) || r.GraphemeLength != uniseg.GraphemeClusterCount(r.Output) {
			t.Fatal("output length invariant")
		}
		if displaytext.Preprocess(r.Output, options).Output != r.Output {
			t.Fatalf("not idempotent: %q", s)
		}
	})
}

func TestTrimWhitespace(t *testing.T) {
	// Unicode 15.0.0 White_Space 的全部 25 个码点。
	whitespace := "\t\n\v\f\r \u0085\u00A0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200A\u2028\u2029\u202F\u205F\u3000"
	for _, r := range whitespace {
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			input := string(r) + "A" + string(r) + "B" + string(r)
			for _, trim := range []bool{false, true} {
				want := input
				if trim {
					want = "A" + string(r) + "B"
				}
				got := displaytext.Preprocess(input, displaytext.Options{TrimWhitespace: trim})
				if got.Output != want {
					t.Fatalf("trim=%v: got %q, want %q", trim, got.Output, want)
				}
			}
		})
	}
	for _, input := range []string{whitespace, "\r\n\r\n", ""} {
		if got := displaytext.Preprocess(input, displaytext.Options{TrimWhitespace: true}); got.Output != "" {
			t.Fatalf("not empty: %q", got.Output)
		}
	}
	for _, input := range []string{" \uFE0Fa", "\u00A0\u0301a", "a \uFE0F"} {
		if got := displaytext.Preprocess(input, displaytext.Options{TrimWhitespace: true}); got.Output != input {
			t.Fatalf("split cluster: %q -> %q", input, got.Output)
		}
	}
}

// 可见内容在全部 16 种配置下均应逐字节保留，不能把符号或正常字素当作复制噪声。
func TestPreprocessPreservesUserContent(t *testing.T) {
	for _, input := range []string{
		"•标题", "★标题★", "“标题”", "《书名》", "[昵称]", "@用户#标签", "-前后缀_",
		"Hello, world!", "你好，世界！", "日本語のタイトル", "한국어", "العربية", "עברית", "ภาษาไทย", "हिन्दी",
		"می\u200cروم", "क्\u200dष", "한", "汉\U000E0100",
		"é", "e\u0301", "ＡＢＣ１２３", "①ﬁ²", "x²+y₁≤∞", "€99.00", "$10.00",
		"👨‍👩‍👧‍👦", "👩🏽‍💻", "🏳️‍🌈", "🇨🇳🇺🇸", "1️⃣#️⃣*️⃣", "👍🏽", "☀︎☀️",
		"🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F",
		"https://example.com/a?q=1&b=2#part", "User+tag@example.com", "C++/C#", "<b>文本</b>", "&nbsp;", "&#8203;",
	} {
		t.Run(input, func(t *testing.T) {
			for bits := 0; bits < 16; bits++ {
				options := displaytext.Options{CollapseSpaces: bits&1 != 0, NewlinesToSpace: bits&2 != 0, WhitespaceToSpace: bits&4 != 0, TrimWhitespace: bits&8 != 0}
				result := displaytext.Preprocess(input, options)
				if result.Output != input || result.Original != input {
					t.Fatalf("options=%+v: changed %q into %q", options, input, result.Output)
				}
			}
		})
	}
}
