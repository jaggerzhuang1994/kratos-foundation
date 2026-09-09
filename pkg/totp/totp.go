// Package totp implements RFC 6238 TOTP codes compatible with Google
// Authenticator and other otpauth applications.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 and Google Authenticator default to HMAC-SHA-1.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPeriod      = 30 * time.Second
	defaultDigits      = 6
	defaultSecretBytes = 20
	maximumSkew        = 2
)

// Authenticator generates and validates TOTP codes.
type Authenticator struct {
	secret string
	key    []byte
	period time.Duration
	digits int
	skew   int
}

// Option configures an Authenticator.
type Option func(*Authenticator) error

// WithPeriod 设置 TOTP 时间步长，RFC 6238 推荐 30 秒。
func WithPeriod(period time.Duration) Option {
	return func(authenticator *Authenticator) error {
		if period <= 0 || period%time.Second != 0 {
			return errors.New("TOTP period must be a positive whole number of seconds")
		}
		authenticator.period = period
		return nil
	}
}

// WithDigits 设置验证码位数，Google Authenticator 通常使用 6 位。
func WithDigits(digits int) Option {
	return func(authenticator *Authenticator) error {
		if digits != 6 && digits != 8 {
			return errors.New("TOTP digits must be 6 or 8")
		}
		authenticator.digits = digits
		return nil
	}
}

// WithSkew 设置当前时间步前后可接受的漂移步数。
func WithSkew(skew int) Option {
	return func(authenticator *Authenticator) error {
		if skew < 0 || skew > maximumSkew {
			return fmt.Errorf("TOTP skew must be between 0 and %d", maximumSkew)
		}
		authenticator.skew = skew
		return nil
	}
}

// New 校验 Base32 密钥并构造验证器。
func New(secret string, options ...Option) (*Authenticator, error) {
	secret = normalizeSecret(secret)
	key, err := decodeSecret(secret)
	if err != nil {
		return nil, err
	}
	authenticator := &Authenticator{
		secret: secret,
		key:    key,
		period: defaultPeriod,
		digits: defaultDigits,
		skew:   1,
	}
	for _, apply := range options {
		if apply == nil {
			return nil, errors.New("TOTP option is nil")
		}
		if err = apply(authenticator); err != nil {
			return nil, err
		}
	}
	return authenticator, nil
}

// GenerateSecret 返回密码学安全的无填充 Base32 随机密钥。
func GenerateSecret() (string, error) {
	secret := make([]byte, defaultSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// Secret 返回标准化后的 Base32 密钥。
func (authenticator *Authenticator) Secret() string { return authenticator.secret }

// Current 返回当前时刻的验证码。
func (authenticator *Authenticator) Current() string {
	return authenticator.CodeAt(time.Now())
}

// CodeAt 返回指定时刻的验证码。
func (authenticator *Authenticator) CodeAt(at time.Time) string {
	counter := uint64(at.Unix() / int64(authenticator.period/time.Second))
	return hotp(authenticator.key, counter, authenticator.digits)
}

// Authenticate 按当前时刻和配置的漂移窗口验证代码。
func (authenticator *Authenticator) Authenticate(code string) bool {
	return authenticator.AuthenticateAt(code, time.Now())
}

// AuthenticateAt 按指定时刻验证代码。
func (authenticator *Authenticator) AuthenticateAt(code string, at time.Time) bool {
	if len(code) != authenticator.digits {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	step := at.Unix() / int64(authenticator.period/time.Second)
	for offset := -authenticator.skew; offset <= authenticator.skew; offset++ {
		candidateStep := step + int64(offset)
		if candidateStep < 0 {
			continue
		}
		candidate := hotp(authenticator.key, uint64(candidateStep), authenticator.digits)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// ProvisionURI 返回适合生成二维码的 otpauth URI。
func (authenticator *Authenticator) ProvisionURI(account string) (string, error) {
	return authenticator.ProvisionURIWithIssuer(account, "")
}

// ProvisionURIWithIssuer 返回带可选签发者的 otpauth URI。
func (authenticator *Authenticator) ProvisionURIWithIssuer(account, issuer string) (string, error) {
	account = strings.TrimSpace(account)
	issuer = strings.TrimSpace(issuer)
	if account == "" {
		return "", errors.New("TOTP account is empty")
	}
	label := account
	if issuer != "" {
		label = issuer + ":" + account
	}
	query := url.Values{
		"secret": {authenticator.secret},
		"period": {strconv.FormatInt(int64(authenticator.period/time.Second), 10)},
		"digits": {strconv.Itoa(authenticator.digits)},
	}
	if issuer != "" {
		query.Set("issuer", issuer)
	}
	uri := &url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + label, RawQuery: query.Encode()}
	return uri.String(), nil
}

// hotp 根据 HMAC-SHA-1 动态截断规则计算计数型验证码。
func hotp(key []byte, counter uint64, digits int) string {
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	modulus := uint32(1)
	for range digits {
		modulus *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%modulus)
}

// normalizeSecret 统一 Base32 密钥的大小写和填充形式。
func normalizeSecret(secret string) string {
	return strings.TrimRight(strings.ToUpper(strings.TrimSpace(secret)), "=")
}

// decodeSecret 解码并校验 Base32 密钥的最小熵长度。
func decodeSecret(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("TOTP secret is empty")
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("decode TOTP Base32 secret: %w", err)
	}
	if len(key) < 10 {
		return nil, errors.New("TOTP secret must decode to at least 10 bytes")
	}
	return key, nil
}
