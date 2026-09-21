package validation

import (
	"errors"
	"fmt"
)

var (
	// ErrPasswordInvalidConfig 表示密码校验配置无效。
	ErrPasswordInvalidConfig = errors.New("invalid password validation config")
	// ErrPasswordTooShort 表示密码长度不足。
	ErrPasswordTooShort = errors.New("password too short")
	// ErrPasswordTooLong 表示密码长度超出上限。
	ErrPasswordTooLong = errors.New("password too long")
	// ErrPasswordInvalidCharacter 表示密码包含 ASCII 0x20–0x7E 以外的字符。
	ErrPasswordInvalidCharacter = errors.New("password contains an invalid character: only ASCII 0x20-0x7E is allowed")
	// ErrPasswordInsufficientTypes 表示密码字符类别数不足。
	ErrPasswordInsufficientTypes = errors.New("password has insufficient character types")
)

// IsValidPassword 校验密码长度及字符类别数，长度范围为闭区间 [minLength, maxLength]。
// 仅允许 ASCII 0x20–0x7E；大写、小写、数字、特殊字符各算一类，空格属于特殊字符。
// 返回错误支持通过 errors.Is 判断本文件定义的 ErrPassword 系列错误。
// 不裁剪空白；要求 1 <= minLength <= maxLength、1 <= minTypes <= 4，成功返回 nil，失败返回具体原因且不包含密码原文。
func IsValidPassword(password string, minLength, maxLength, minTypes int) error {
	// 无效配置直接拒绝，避免意外放宽密码约束。
	if minLength < 1 || maxLength < minLength {
		return fmt.Errorf("%w: require 1 <= minLength <= maxLength, got %d and %d", ErrPasswordInvalidConfig, minLength, maxLength)
	}
	if minTypes < 1 || minTypes > 4 {
		return fmt.Errorf("%w: minTypes must be between 1 and 4, got %d", ErrPasswordInvalidConfig, minTypes)
	}
	if len(password) < minLength {
		return fmt.Errorf("%w: minimum %d bytes", ErrPasswordTooShort, minLength)
	}
	if len(password) > maxLength {
		return fmt.Errorf("%w: maximum %d bytes", ErrPasswordTooLong, maxLength)
	}

	var hasLower, hasUpper, hasDigit, hasSpecial bool
	for i := 0; i < len(password); i++ {
		c := password[i]
		if c < 0x20 || c > 0x7E {
			return ErrPasswordInvalidCharacter
		}
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	n := 0
	if hasLower {
		n++
	}
	if hasUpper {
		n++
	}
	if hasDigit {
		n++
	}
	if hasSpecial {
		n++
	}
	if n < minTypes {
		return fmt.Errorf("%w: require at least %d (uppercase, lowercase, digit, special); got %d", ErrPasswordInsufficientTypes, minTypes, n)
	}
	return nil
}
