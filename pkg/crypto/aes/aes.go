// Package aes 提供带 PKCS#7 填充的 AES-CBC 加解密。
//
// 安全边界：CBC 只提供机密性，不提供完整性和真实性校验。密文被篡改不会被
// 检出；如果解密失败的原因会以任何形式反馈给攻击者，CBC 还可能被当作 padding
// oracle 利用。
//
// 本包的定位是数据库字段的静态加密，威胁模型是数据库文件或备份泄漏，而不是能反复
// 交互并观察解密结果的攻击者。凡是密文会经不可信通道传输、或攻击者可以观测解密
// 成败的场景，都应改用 AEAD（例如 crypto/cipher 的 GCM）。
package aes

import (
	"bytes"
	stdaes "crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher 是数据库加密插件和调用方共用的 AES 算法接口。
type Cipher interface {
	Encrypt(raw, key []byte) ([]byte, error)
	Decrypt(encrypted, key []byte) ([]byte, error)
	EncryptString(raw, key string) (string, error)
	DecryptString(encrypted, key string) (string, error)
}

// CBC 使用随机 IV；密文格式为 IV || PKCS#7(AES-CBC(plaintext))。
type CBC struct{}

// Encrypt 使用 AES-CBC 加密字节并在密文前放置随机 IV。
func (CBC) Encrypt(raw, key []byte) ([]byte, error) {
	block, err := stdaes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	padded := pkcs7Pad(raw, block.BlockSize())
	result := make([]byte, block.BlockSize()+len(padded))
	iv := result[:block.BlockSize()]
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate AES-CBC IV: %w", err)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(result[block.BlockSize():], padded)
	return result, nil
}

// Decrypt 解密带 IV 前缀的 AES-CBC 密文。
func (CBC) Decrypt(encrypted, key []byte) ([]byte, error) {
	block, err := stdaes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	blockSize := block.BlockSize()
	if len(encrypted) < blockSize*2 || (len(encrypted)-blockSize)%blockSize != 0 {
		return nil, fmt.Errorf("invalid AES-CBC ciphertext length %d", len(encrypted))
	}
	iv := encrypted[:blockSize]
	result := make([]byte, len(encrypted)-blockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(result, encrypted[blockSize:])
	result, err = pkcs7Unpad(result, blockSize)
	if err != nil {
		return nil, fmt.Errorf("decrypt AES-CBC: %w", err)
	}
	return result, nil
}

// EncryptString 加密字符串并返回 Base64 密文。
func (cipher CBC) EncryptString(raw, key string) (string, error) {
	encrypted, err := cipher.Encrypt([]byte(raw), []byte(key))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// DecryptString 解码 Base64 后解密字符串密文。
func (cipher CBC) DecryptString(encrypted, key string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decode AES-CBC ciphertext: %w", err)
	}
	plain, err := cipher.Decrypt(data, []byte(key))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

var errInvalidPadding = errors.New("invalid PKCS#7 padding")

// pkcs7Pad 按块大小追加 PKCS#7 填充。
func pkcs7Pad(src []byte, blockSize int) []byte {
	padding := blockSize - len(src)%blockSize
	result := make([]byte, 0, len(src)+padding)
	result = append(result, src...)
	return append(result, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

// pkcs7Unpad 校验并移除 PKCS#7 填充。
func pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	if len(src) == 0 || len(src)%blockSize != 0 {
		return nil, fmt.Errorf("%w: invalid plaintext length %d", errInvalidPadding, len(src))
	}
	padding := int(src[len(src)-1])
	if padding == 0 || padding > blockSize || padding > len(src) {
		return nil, errInvalidPadding
	}
	for _, value := range src[len(src)-padding:] {
		if int(value) != padding {
			return nil, errInvalidPadding
		}
	}
	return src[:len(src)-padding], nil
}
