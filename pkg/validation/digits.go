package validation

import (
	"errors"
	"fmt"
)

var (
	// ErrDigitsInvalidConfig 表示纯数字校验的长度范围配置无效。
	ErrDigitsInvalidConfig = errors.New("invalid digits validation config")
	// ErrDigitsTooShort 表示输入长度不足。
	ErrDigitsTooShort = errors.New("digits input too short")
	// ErrDigitsTooLong 表示输入长度超出上限。
	ErrDigitsTooLong = errors.New("digits input too long")
	// ErrDigitsInvalidCharacter 表示输入包含 ASCII 数字以外的字符。
	ErrDigitsInvalidCharacter = errors.New("digits input must contain only ASCII 0-9")
)

// IsValidDigits 校验 input 是否仅包含 ASCII 数字，且长度位于闭区间 [minLength, maxLength]。
// 允许前导零，不裁剪空白；要求 1 <= minLength <= maxLength。
// 成功返回 nil，失败返回可通过 errors.Is 识别的 ErrDigits 系列错误，不包含输入原文。
func IsValidDigits(input string, minLength, maxLength int) error {
	// 按配置、长度、字符顺序返回首个错误，避免无效配置意外放宽约束。
	if minLength < 1 || maxLength < minLength {
		return fmt.Errorf("%w: require 1 <= minLength <= maxLength, got %d and %d", ErrDigitsInvalidConfig, minLength, maxLength)
	}
	if len(input) < minLength {
		return fmt.Errorf("%w: minimum %d bytes", ErrDigitsTooShort, minLength)
	}
	if len(input) > maxLength {
		return fmt.Errorf("%w: maximum %d bytes", ErrDigitsTooLong, maxLength)
	}
	for i := range len(input) {
		if input[i] < '0' || input[i] > '9' {
			return ErrDigitsInvalidCharacter
		}
	}
	return nil
}
