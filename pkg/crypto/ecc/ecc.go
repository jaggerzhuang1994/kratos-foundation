// Package ecc 提供 P-256/P-384/P-521 ECIES 加解密和 ECDSA 密钥编解码。
package ecc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
)

// GenerateKey 生成 P-256 ECDSA 密钥对。
func GenerateKey() (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate EC private key: %w", err)
	}
	return privateKey, &privateKey.PublicKey, nil
}

// EncodePrivateKey 把 EC 私钥编码为 PEM。
func EncodePrivateKey(privateKey *ecdsa.PrivateKey) (string, error) {
	if privateKey == nil {
		return "", errors.New("EC private key is nil")
	}
	der, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("marshal EC private key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

// EncodePublicKey 把 EC 公钥编码为 PEM。
func EncodePublicKey(publicKey *ecdsa.PublicKey) (string, error) {
	if err := validatePublicKey(publicKey); err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("marshal EC public key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PUBLIC KEY", Bytes: der})), nil
}

// ParsePrivateKey 从 PEM 解析 EC 私钥。
func ParsePrivateKey(encoded string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("decode EC private key PEM")
	}
	if block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected EC private key PEM type %q", block.Type)
	}
	privateKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse EC private key: %w", err)
	}
	return privateKey, nil
}

// ParsePublicKey 从 PEM 解析 EC 公钥。
func ParsePublicKey(encoded string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("decode EC public key PEM")
	}
	if block.Type != "EC PUBLIC KEY" {
		return nil, fmt.Errorf("unexpected EC public key PEM type %q", block.Type)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse EC public key: %w", err)
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("PEM contains %T, not an ECDSA public key", parsed)
	}
	if err = validatePublicKey(publicKey); err != nil {
		return nil, err
	}
	return publicKey, nil
}

// validatePublicKey 校验 EC 公钥结构和曲线坐标。
func validatePublicKey(publicKey *ecdsa.PublicKey) error {
	if publicKey == nil || publicKey.Curve == nil || publicKey.X == nil || publicKey.Y == nil {
		return errors.New("EC public key is nil or incomplete")
	}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return errors.New("EC public key is not on its curve")
	}
	return nil
}

var errInvalidCiphertext = errors.New("invalid ECIES ciphertext")

type eciesParams struct {
	hash   func() hash.Hash
	keyLen int
}

// paramsForCurve 返回与参考 ECIES 实现兼容的曲线参数。
func paramsForCurve(curve elliptic.Curve) (eciesParams, error) {
	if curve == nil || curve.Params() == nil {
		return eciesParams{}, errors.New("EC curve is nil")
	}
	switch curve.Params().BitSize {
	case 256:
		return eciesParams{hash: sha256.New, keyLen: 16}, nil
	case 384:
		return eciesParams{hash: sha512.New384, keyLen: 24}, nil
	case 521:
		return eciesParams{hash: sha512.New, keyLen: 32}, nil
	default:
		return eciesParams{}, fmt.Errorf("unsupported ECIES curve size %d", curve.Params().BitSize)
	}
}

// EncryptToBase64StringByPubHex 使用 64 字节 P-256 X||Y 十六进制公钥加密。
func EncryptToBase64StringByPubHex(plainText, publicHex string) (string, error) {
	if len(publicHex) != 128 {
		return "", fmt.Errorf("invalid public key length %d", len(publicHex))
	}
	encoded, err := hex.DecodeString(publicHex)
	if err != nil {
		return "", fmt.Errorf("decode public key hex: %w", err)
	}
	publicKey := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(encoded[:32]),
		Y:     new(big.Int).SetBytes(encoded[32:]),
	}
	return EncryptToBase64String(plainText, publicKey)
}

// EncryptToBase64String 使用 ECIES 加密字符串并返回 Base64 密文。
func EncryptToBase64String(plainText string, publicKey *ecdsa.PublicKey) (string, error) {
	ciphertext, err := Encrypt(plainText, publicKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Encrypt 使用经过认证的 ECIES 格式 R || IV || ciphertext || HMAC。
func Encrypt(plainText string, publicKey *ecdsa.PublicKey) ([]byte, error) {
	if err := validatePublicKey(publicKey); err != nil {
		return nil, err
	}
	params, err := paramsForCurve(publicKey.Curve)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdsa.GenerateKey(publicKey.Curve, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral EC key: %w", err)
	}
	sharedX, _ := publicKey.Curve.ScalarMult(publicKey.X, publicKey.Y, ephemeral.D.Bytes())
	if sharedX == nil {
		return nil, errors.New("ECIES shared key is point at infinity")
	}
	encryptionKey, macKey := deriveKeys(params, sharedX)

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create ECIES AES cipher: %w", err)
	}
	message := []byte(plainText)
	encryptedMessage := make([]byte, block.BlockSize()+len(message))
	iv := encryptedMessage[:block.BlockSize()]
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate ECIES IV: %w", err)
	}
	cipher.NewCTR(block, iv).XORKeyStream(encryptedMessage[block.BlockSize():], message)
	ephemeralPublic := elliptic.Marshal(publicKey.Curve, ephemeral.X, ephemeral.Y)
	tag := messageTag(params.hash, macKey, ephemeralPublic, encryptedMessage)

	result := make([]byte, 0, len(ephemeralPublic)+len(encryptedMessage)+len(tag))
	result = append(result, ephemeralPublic...)
	result = append(result, encryptedMessage...)
	return append(result, tag...), nil
}

// DecryptFromBase64String 解码 Base64 后使用 ECIES 私钥解密。
func DecryptFromBase64String(ciphertextBase64 string, privateKey *ecdsa.PrivateKey) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return nil, fmt.Errorf("decode ECIES base64 ciphertext: %w", err)
	}
	return Decrypt(ciphertext, privateKey)
}

// Decrypt 验证 ECIES HMAC 并解密密文。
func Decrypt(ciphertext []byte, privateKey *ecdsa.PrivateKey) ([]byte, error) {
	if privateKey == nil || privateKey.Curve == nil || privateKey.D == nil {
		return nil, errors.New("EC private key is nil or incomplete")
	}
	params, err := paramsForCurve(privateKey.Curve)
	if err != nil {
		return nil, err
	}
	publicLength := 1 + 2*((privateKey.Curve.Params().BitSize+7)/8)
	hashLength := params.hash().Size()
	if len(ciphertext) < publicLength+aes.BlockSize+hashLength {
		return nil, errInvalidCiphertext
	}
	ephemeralX, ephemeralY := elliptic.Unmarshal(privateKey.Curve, ciphertext[:publicLength])
	if ephemeralX == nil || ephemeralY == nil {
		return nil, errInvalidCiphertext
	}
	sharedX, _ := privateKey.Curve.ScalarMult(ephemeralX, ephemeralY, privateKey.D.Bytes())
	if sharedX == nil {
		return nil, errInvalidCiphertext
	}
	encryptionKey, macKey := deriveKeys(params, sharedX)
	encryptedMessage := ciphertext[publicLength : len(ciphertext)-hashLength]
	expectedTag := messageTag(params.hash, macKey, ciphertext[:publicLength], encryptedMessage)
	if subtle.ConstantTimeCompare(ciphertext[len(ciphertext)-hashLength:], expectedTag) != 1 {
		return nil, errInvalidCiphertext
	}
	if len(encryptedMessage) < aes.BlockSize {
		return nil, errInvalidCiphertext
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create ECIES AES cipher: %w", err)
	}
	plain := make([]byte, len(encryptedMessage)-aes.BlockSize)
	cipher.NewCTR(block, encryptedMessage[:aes.BlockSize]).XORKeyStream(plain, encryptedMessage[aes.BlockSize:])
	return plain, nil
}

// deriveKeys 从 ECDH 共享点派生加密密钥和认证密钥。
func deriveKeys(params eciesParams, sharedX *big.Int) ([]byte, []byte) {
	sharedLength := (sharedX.BitLen() + 7) / 8
	curveLength := params.keyLen * 2
	shared := make([]byte, curveLength)
	sharedBytes := sharedX.Bytes()
	if sharedLength > len(shared) {
		sharedBytes = sharedBytes[len(sharedBytes)-len(shared):]
	}
	copy(shared[len(shared)-len(sharedBytes):], sharedBytes)
	derived := concatKDF(params.hash(), shared, 2*params.keyLen)
	encryptionKey := derived[:params.keyLen]
	macSeed := derived[params.keyLen:]
	macHash := params.hash()
	_, _ = macHash.Write(macSeed)
	return encryptionKey, macHash.Sum(nil)
}

// concatKDF 实现 NIST SP 800-56 连接密钥派生函数。
func concatKDF(digest hash.Hash, shared []byte, length int) []byte {
	result := make([]byte, 0, length+digest.Size())
	counterBytes := make([]byte, 4)
	for counter := uint32(1); len(result) < length; counter++ {
		binary.BigEndian.PutUint32(counterBytes, counter)
		digest.Reset()
		_, _ = digest.Write(counterBytes)
		_, _ = digest.Write(shared)
		result = digest.Sum(result)
	}
	return result[:length]
}

// messageTag 计算 ECIES 临时公钥、IV 和密文正文的 HMAC 标签。
func messageTag(hashFunc func() hash.Hash, key []byte, messages ...[]byte) []byte {
	mac := hmac.New(hashFunc, key)
	for _, message := range messages {
		_, _ = mac.Write(message)
	}
	return mac.Sum(nil)
}
