package validation

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

var (
	// ErrEmailEmpty 表示邮箱输入为空。
	ErrEmailEmpty = errors.New("email is empty")
	// ErrEmailTooLong 表示邮箱超过 254 字节。
	ErrEmailTooLong = errors.New("email exceeds 254 bytes")
	// ErrEmailInvalidFormat 表示邮箱语法解析失败。
	ErrEmailInvalidFormat = errors.New("invalid email format")
	// ErrEmailNotPlainAddress 表示输入包含显示名、注释、引号或额外空白等非纯地址内容。
	ErrEmailNotPlainAddress = errors.New("email must be a plain address without display name, comments, quotes or surrounding whitespace")
	// ErrEmailInvalidDomain 表示域名标签的长度、字符或连字符位置不合法。
	ErrEmailInvalidDomain = errors.New("invalid email domain")
)

// NormalizeEmail 去除首尾空白，提取单个邮件头地址中的邮箱，并整体转换为小写。
// 解析失败时仅去首尾空白并转小写；不处理完整邮件头字段或地址列表，不代替 IsValidEmail 校验。
func NormalizeEmail(email string) string {
	email = strings.TrimSpace(email)
	// 仅在完整输入能解析为单个地址时提取，避免从无效输入或地址列表中误取邮箱。
	if address, err := mail.ParseAddress(email); err == nil {
		email = address.Address
	}
	return strings.ToLower(email)
}

// IsValidEmail 校验不带显示名、注释或引号的邮箱地址，不验证邮箱存在或域名可达。
// 本地部分允许 Unicode，域名仅允许 ASCII 标签。
// 地址最多 254 字节；域名允许单段，每段 1–63 字节。
// 不校验域名后缀或 DNS；不接受邮件头格式、IP 字面量或原始 Unicode 域名。
// 成功返回 nil；失败返回可通过 errors.Is 识别的 ErrEmail 系列错误，不包含邮箱原文。
func IsValidEmail(email string) error {
	if len(email) == 0 {
		return ErrEmailEmpty
	}
	if len(email) > 254 {
		return ErrEmailTooLong
	}
	// ParseAddress 也接受邮件头格式；要求解析结果等于原输入，以排除显示名、注释和引号。
	address, err := mail.ParseAddress(email)
	if err != nil {
		// 不透传解析器错误，避免其错误文本携带邮箱输入。
		return ErrEmailInvalidFormat
	}
	if address.Name != "" || address.Address != email {
		return ErrEmailNotPlainAddress
	}
	_, domain, _ := strings.Cut(email, "@")
	// 邮件语法解析不保证 DNS 标签有效，额外限制长度、字符和连字符边界。
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("%w: each label must contain 1-63 bytes", ErrEmailInvalidDomain)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: labels must not start or end with a hyphen", ErrEmailInvalidDomain)
		}
		for i := range len(label) {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
				continue
			default:
				return fmt.Errorf("%w: labels may only contain ASCII letters, digits and hyphens", ErrEmailInvalidDomain)
			}
		}
	}
	return nil
}
