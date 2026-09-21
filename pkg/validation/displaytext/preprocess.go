// Package displaytext 提供基于 Unicode 15.0.0 的展示文本预处理。
package displaytext

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Options 的零值保留换行和其他空白，不压缩空格，也不 trim。
// 所有模式都会删除符合条件的控制/默认可忽略簇。
type Options struct {
	TrimWhitespace    bool // 去除首尾全为 Unicode White_Space 的簇；不拆分簇。
	CollapseSpaces    bool // 连续独立 U+0020 簇压缩成一个；不拆分簇。
	NewlinesToSpace   bool // 连续 CR/LF 转成一个 U+0020；false 时不转换（首尾仍可被 trim）。
	WhitespaceToSpace bool // 全为空白的簇转成 U+0020；CR/LF 由上一个选项控制。
}

// Result 的所有长度均针对 Output。UnicodeLength 是码点数，不是 UTF-16 单元数。
type Result struct {
	Original       string `json:"original"`
	Output         string `json:"output"`
	ByteLength     int    `json:"byte_length"`
	UnicodeLength  int    `json:"unicode_length"`
	GraphemeLength int    `json:"grapheme_length"`
}

// Preprocess 保留 Original 的原始字节。Output 中每段连续非法 UTF-8
// 替换为一个 U+FFFD。不会执行 NFC/NFKC，也不会删除正常簇内的 ZWJ/VS。
func Preprocess(input string, options Options) Result {
	output := strings.ToValidUTF8(input, "\uFFFD")
	// 删除、trim 和拼接都可能改变簇边界；执行到稳定以保证输出幂等。
	// 每次改变均缩短 UTF-8 字节长度，或将非 ASCII 空白替换成空格，故会终止。
	for {
		next := preprocessOnce(output, options)
		if next == output {
			break
		}
		output = next
	}
	return Result{
		Original: input, Output: output,
		ByteLength: len(output), UnicodeLength: utf8.RuneCountInString(output),
		GraphemeLength: uniseg.GraphemeClusterCount(output),
	}
}

func preprocessOnce(s string, options Options) string {
	if options.NewlinesToSpace {
		var b strings.Builder
		b.Grow(len(s))
		inRun := false
		for _, r := range s {
			if r == '\r' || r == '\n' {
				if !inRun {
					b.WriteByte(' ')
				}
				inRun = true
			} else {
				inRun = false
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	var b strings.Builder
	b.Grow(len(s))
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		// CR/LF 和 CRLF 均为独立 EGC；保留开关优先于空白/控制规则。
		if !options.NewlinesToSpace && (cluster == "\r" || cluster == "\n" || cluster == "\r\n") {
			b.WriteString(cluster)
			continue
		}
		allWhitespace, allRemovable := true, true
		for _, r := range cluster {
			allWhitespace = allWhitespace && isWhitespace(r)
			allRemovable = allRemovable && (isDefaultIgnorable(r) || isControl(r))
		}
		switch {
		case allWhitespace:
			if options.WhitespaceToSpace {
				b.WriteByte(' ')
			} else {
				b.WriteString(cluster)
			}
		case allRemovable:
			// 删除整个簇。
		default:
			b.WriteString(cluster)
		}
	}
	// 重新分段：拼接后的簇边界可能变化。trim/collapse 也只操作完整簇。
	s = b.String()
	if options.TrimWhitespace {
		g = uniseg.NewGraphemes(s)
		start, end := -1, 0
		for g.Next() {
			allWhitespace := true
			for _, r := range g.Str() {
				if !isWhitespace(r) {
					allWhitespace = false
					break
				}
			}
			if !allWhitespace {
				from, to := g.Positions()
				if start < 0 {
					start = from
				}
				end = to
			}
		}
		if start < 0 {
			return ""
		}
		s = s[start:end]
	}
	if !options.CollapseSpaces {
		return s
	}
	b.Reset()
	b.Grow(len(s))
	g = uniseg.NewGraphemes(s)
	previousSpace := false
	for g.Next() {
		cluster := g.Str()
		space := cluster == " "
		if !space || !previousSpace {
			b.WriteString(cluster)
		}
		previousSpace = space
	}
	return b.String()
}

// 固定 Unicode 15.0.0 属性，与 uniseg v0.4.7 一致，避免 Go 升级改变行为。
// White_Space: https://www.unicode.org/Public/15.0.0/ucd/PropList.txt
func isWhitespace(r rune) bool {
	return r >= 0x0009 && r <= 0x000D || r == 0x0020 || r == 0x0085 ||
		r == 0x00A0 || r == 0x1680 || r >= 0x2000 && r <= 0x200A ||
		r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000
}

func isControl(r rune) bool {
	return r >= 0 && r <= 0x001F || r >= 0x007F && r <= 0x009F
}

// Default_Ignorable_Code_Point，来自官方 DerivedCoreProperties.txt，含保留码点。
// https://www.unicode.org/Public/15.0.0/ucd/DerivedCoreProperties.txt
func isDefaultIgnorable(r rune) bool {
	return r == 0x00AD || r == 0x034F || r == 0x061C ||
		r >= 0x115F && r <= 0x1160 || r >= 0x17B4 && r <= 0x17B5 ||
		r >= 0x180B && r <= 0x180F || r >= 0x200B && r <= 0x200F ||
		r >= 0x202A && r <= 0x202E || r >= 0x2060 && r <= 0x206F ||
		r == 0x3164 || r >= 0xFE00 && r <= 0xFE0F || r == 0xFEFF ||
		r == 0xFFA0 || r >= 0xFFF0 && r <= 0xFFF8 ||
		r >= 0x1BCA0 && r <= 0x1BCA3 || r >= 0x1D173 && r <= 0x1D17A ||
		r >= 0xE0000 && r <= 0xE0FFF
}
