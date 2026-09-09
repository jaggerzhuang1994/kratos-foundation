// Package rsa 提供分块 RSA PKCS#1 v1.5 加解密和密钥编解码。
package rsa

import (
	"crypto/rand"
	stdrsa "crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
)

// GenerateKey 生成指定位数的 RSA 密钥对。
func GenerateKey(bits int) (*stdrsa.PrivateKey, *stdrsa.PublicKey, error) {
	privateKey, err := stdrsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, fmt.Errorf("generate RSA private key: %w", err)
	}
	return privateKey, &privateKey.PublicKey, nil
}

// EncodePrivateKey 把 RSA 私钥编码为 PKCS#1 PEM。
func EncodePrivateKey(privateKey *stdrsa.PrivateKey) string {
	if privateKey == nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}))
}

// EncodePublicKey 把 RSA 公钥编码为 PKCS#1 PEM。
func EncodePublicKey(publicKey *stdrsa.PublicKey) string {
	if publicKey == nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(publicKey)}))
}

// ParsePrivateKey 从 PKCS#1 PEM 解析 RSA 私钥。
func ParsePrivateKey(encoded string) (*stdrsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("decode RSA private key PEM")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}
	return privateKey, nil
}

// ParsePublicKey 从 PKCS#1 PEM 解析 RSA 公钥。
func ParsePublicKey(encoded string) (*stdrsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("decode RSA public key PEM")
	}
	publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA public key: %w", err)
	}
	return publicKey, nil
}

// Encrypt 分块使用 RSA PKCS#1 v1.5 公钥加密。
func Encrypt(data []byte, publicKey *stdrsa.PublicKey) ([]byte, error) {
	if publicKey == nil {
		return nil, errors.New("RSA public key is nil")
	}
	return transformBlocks(data, publicKey.Size()-11, func(block []byte) ([]byte, error) {
		return stdrsa.EncryptPKCS1v15(rand.Reader, publicKey, block)
	})
}

// EncryptToBase64String 加密字节并返回 Base64 密文。
func EncryptToBase64String(data []byte, publicKey *stdrsa.PublicKey) (string, error) {
	encrypted, err := Encrypt(data, publicKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// Decrypt 分块使用 RSA PKCS#1 v1.5 私钥解密。
func Decrypt(data []byte, privateKey *stdrsa.PrivateKey) ([]byte, error) {
	if privateKey == nil {
		return nil, errors.New("RSA private key is nil")
	}
	if len(data) == 0 || len(data)%privateKey.Size() != 0 {
		return nil, fmt.Errorf("invalid RSA ciphertext length %d", len(data))
	}
	return transformBlocks(data, privateKey.Size(), func(block []byte) ([]byte, error) {
		return stdrsa.DecryptPKCS1v15(rand.Reader, privateKey, block)
	})
}

// DecryptFromBase64String 解码 Base64 后解密 RSA 密文。
func DecryptFromBase64String(encoded string, privateKey *stdrsa.PrivateKey) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode RSA base64 ciphertext: %w", err)
	}
	return Decrypt(data, privateKey)
}

// transformBlocks 按固定大小顺序转换所有数据块。
func transformBlocks(data []byte, blockSize int, transform func([]byte) ([]byte, error)) ([]byte, error) {
	if blockSize <= 0 {
		return nil, fmt.Errorf("invalid RSA block size %d", blockSize)
	}
	blocks := (len(data) + blockSize - 1) / blockSize
	if blocks == 0 {
		blocks = 1
	}
	result := make([]byte, 0, blocks*blockSize)
	for start := 0; start < len(data) || (len(data) == 0 && start == 0); start += blockSize {
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		transformed, err := transform(data[start:end])
		if err != nil {
			return nil, err
		}
		result = append(result, transformed...)
		if len(data) == 0 {
			break
		}
	}
	return result, nil
}
