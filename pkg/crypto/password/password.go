// Package password provides versioned Argon2id password hashing in PHC string
// format. It intentionally does not expose application-specific two-stage
// client/server password protocols.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	maximumMemoryKiB   = 256 * 1024
	maximumIterations  = 20
	maximumParallelism = 16
	maximumPHCFieldLen = 64
)

// Argon2idParams controls Argon2id cost and output sizes.
type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams 返回 RFC 9106 面向内存受限场景的推荐参数。
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 4,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// HashPassword 使用默认 Argon2id 参数哈希密码。
func HashPassword(password []byte) (string, error) {
	return HashPasswordWithParams(password, DefaultArgon2idParams())
}

// HashPasswordWithParams 使用指定参数返回 Argon2id PHC 字符串。
func HashPasswordWithParams(password []byte, params Argon2idParams) (string, error) {
	if err := params.validate(); err != nil {
		return "", err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate Argon2id salt: %w", err)
	}
	key := argon2.IDKey(
		password,
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		params.KeyLength,
	)
	base64Encoding := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Iterations,
		params.Parallelism,
		base64Encoding.EncodeToString(salt),
		base64Encoding.EncodeToString(key),
	), nil
}

// VerifyPassword 对照 Argon2id PHC 字符串验证密码，并在分配内存前限制不可信参数。
func VerifyPassword(password []byte, encoded string) (bool, error) {
	params, salt, expected, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey(
		password,
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// NeedsRehash 判断已有 PHC 字符串是否需要按目标参数重新哈希。
func NeedsRehash(encoded string, desired Argon2idParams) (bool, error) {
	if err := desired.validate(); err != nil {
		return false, err
	}
	actual, _, _, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	return actual != desired, nil
}

// validate 校验 Argon2id 参数的安全上下限。
func (params Argon2idParams) validate() error {
	if params.Iterations == 0 || params.Iterations > maximumIterations {
		return fmt.Errorf("argon2id iterations must be between 1 and %d", maximumIterations)
	}
	if params.Parallelism == 0 || params.Parallelism > maximumParallelism {
		return fmt.Errorf("argon2id parallelism must be between 1 and %d", maximumParallelism)
	}
	if params.MemoryKiB < 8*uint32(params.Parallelism) || params.MemoryKiB > maximumMemoryKiB {
		return fmt.Errorf(
			"argon2id memory must be between %d and %d KiB",
			8*uint32(params.Parallelism),
			maximumMemoryKiB,
		)
	}
	if params.SaltLength < 8 || params.SaltLength > 64 {
		return errors.New("argon2id salt length must be between 8 and 64 bytes")
	}
	if params.KeyLength < 16 || params.KeyLength > 64 {
		return errors.New("argon2id key length must be between 16 and 64 bytes")
	}
	return nil
}

// parsePHC 严格解析 Argon2id PHC 字符串。
func parsePHC(encoded string) (Argon2idParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2idParams{}, nil, nil, errors.New("invalid Argon2id PHC format")
	}
	version, err := parseUint(strings.TrimPrefix(parts[2], "v="), 32)
	if err != nil || version != argon2.Version || !strings.HasPrefix(parts[2], "v=") {
		return Argon2idParams{}, nil, nil, errors.New("unsupported Argon2id version")
	}

	var params Argon2idParams
	costs := strings.Split(parts[3], ",")
	if len(costs) != 3 {
		return params, nil, nil, errors.New("invalid Argon2id cost parameters")
	}
	memory, err := parseNamedUint(costs[0], "m", 32)
	if err != nil {
		return params, nil, nil, err
	}
	iterations, err := parseNamedUint(costs[1], "t", 32)
	if err != nil {
		return params, nil, nil, err
	}
	parallelism, err := parseNamedUint(costs[2], "p", 8)
	if err != nil {
		return params, nil, nil, err
	}
	params.MemoryKiB = uint32(memory)
	params.Iterations = uint32(iterations)
	params.Parallelism = uint8(parallelism)

	decoder := base64.RawStdEncoding.Strict()
	maximumEncodedLength := decoder.EncodedLen(maximumPHCFieldLen)
	if len(parts[4]) > maximumEncodedLength || len(parts[5]) > maximumEncodedLength {
		return params, nil, nil, errors.New("argon2id PHC field exceeds maximum encoded length")
	}
	salt, err := decoder.DecodeString(parts[4])
	if err != nil {
		return params, nil, nil, fmt.Errorf("decode Argon2id salt: %w", err)
	}
	expected, err := decoder.DecodeString(parts[5])
	if err != nil {
		return params, nil, nil, fmt.Errorf("decode Argon2id hash: %w", err)
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(expected))
	if err = params.validate(); err != nil {
		return params, nil, nil, err
	}
	return params, salt, expected, nil
}

// parseNamedUint 解析 PHC 中带名称的无符号整数参数。
func parseNamedUint(value, name string, bits int) (uint64, error) {
	number, found := strings.CutPrefix(value, name+"=")
	if !found {
		return 0, fmt.Errorf("invalid Argon2id %s parameter", name)
	}
	parsed, err := parseUint(number, bits)
	if err != nil {
		return 0, fmt.Errorf("invalid Argon2id %s parameter: %w", name, err)
	}
	return parsed, nil
}

// parseUint 解析非空的十进制无符号整数。
func parseUint(value string, bits int) (uint64, error) {
	if value == "" {
		return 0, errors.New("empty integer")
	}
	return strconv.ParseUint(value, 10, bits)
}
