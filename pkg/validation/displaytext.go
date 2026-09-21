package validation

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

var (
	// ErrDisplayTextInvalidConfig 表示长度范围配置无效。
	ErrDisplayTextInvalidConfig = errors.New("invalid display text length config")
	// ErrDisplayTextInvalidUTF8 表示输入不是有效 UTF-8。
	ErrDisplayTextInvalidUTF8 = errors.New("display text is not valid UTF-8")
	// ErrDisplayTextByteLength 表示 UTF-8 字节数不在范围内。
	ErrDisplayTextByteLength = errors.New("display text byte length out of range")
	// ErrDisplayTextUnicodeLength 表示 Unicode 码点数不在范围内。
	ErrDisplayTextUnicodeLength = errors.New("display text unicode length out of range")
	// ErrDisplayTextGraphemeLength 表示扩展字素簇数不在范围内。
	ErrDisplayTextGraphemeLength = errors.New("display text grapheme length out of range")
)

// LengthRange 是闭区间长度约束；Min、Max 不得为负，Max 为 0 表示不设上限。
// Max 非零时必须不小于 Min；零值不限制长度。
type LengthRange struct {
	Min int
	Max int
}

// DisplayTextOptions 分别限制输入的字节数、Unicode 码点数和扩展字素簇数。
// 零值仅检查有效 UTF-8，允许空字符串。
type DisplayTextOptions struct {
	ByteLength     LengthRange
	UnicodeLength  LengthRange
	GraphemeLength LengthRange
}

// CheckDisplayText 检查 input 的 UTF-8 和长度，不执行预处理或修改输入。
// 按配置、UTF-8、字节数、码点数、字素数顺序返回首个错误；成功返回 nil。
// 错误支持 errors.Is，长度错误包含实际长度和约束，不包含输入文本。
func CheckDisplayText(input string, options DisplayTextOptions) error {
	checks := [...]struct {
		name   string
		bounds LengthRange
		count  func(string) int
		err    error
	}{
		{"byte", options.ByteLength, func(s string) int { return len(s) }, ErrDisplayTextByteLength},
		{"unicode", options.UnicodeLength, utf8.RuneCountInString, ErrDisplayTextUnicodeLength},
		{"grapheme", options.GraphemeLength, uniseg.GraphemeClusterCount, ErrDisplayTextGraphemeLength},
	}
	// 先验证所有配置，确保无效的后续约束不会被输入错误掩盖。
	for _, check := range checks {
		r := check.bounds
		if r.Min < 0 || r.Max < 0 || (r.Max != 0 && r.Max < r.Min) {
			return fmt.Errorf("%w: %s min=%d max=%d", ErrDisplayTextInvalidConfig, check.name, r.Min, r.Max)
		}
	}
	if !utf8.ValidString(input) {
		return ErrDisplayTextInvalidUTF8
	}
	for _, check := range checks {
		r := check.bounds
		if r.Min == 0 && r.Max == 0 {
			continue
		}
		n := check.count(input)
		if n < r.Min || (r.Max != 0 && n > r.Max) {
			return fmt.Errorf("%w: got %d, min=%d max=%d (0 means unlimited)", check.err, n, r.Min, r.Max)
		}
	}
	return nil
}
