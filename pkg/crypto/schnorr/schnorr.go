// Package schnorr implements a non-interactive Schnorr proof of knowledge for
// ECDSA private keys. Its transcript matches the GG18-style proof used by the
// reference repository while relying only on Go's standard library.
package schnorr

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// SchnorrProof 为指定会话生成私钥离散对数知识证明；每次操作必须使用新会话。
func SchnorrProof(
	priKey *ecdsa.PrivateKey,
	session []byte,
) (*big.Int, *big.Int, *big.Int, error) {
	if err := validatePrivateKey(priKey); err != nil {
		return nil, nil, nil, err
	}
	if len(session) == 0 {
		return nil, nil, nil, errors.New("Schnorr session is empty")
	}
	curve := priKey.Curve
	order := curve.Params().N
	nonce, err := randomNonZeroScalar(order)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate Schnorr nonce: %w", err)
	}
	alphaX, alphaY := curve.ScalarBaseMult(nonce.Bytes())
	challenge := challengeFor(
		session,
		order,
		priKey.PublicKey.X,
		priKey.PublicKey.Y,
		curve.Params().Gx,
		curve.Params().Gy,
		alphaX,
		alphaY,
	)
	response := new(big.Int).Mul(challenge, priKey.D)
	response.Add(response, nonce)
	response.Mod(response, order)
	return alphaX, alphaY, response, nil
}

// randomNonZeroScalar 在曲线阶范围内生成非零随机标量。
func randomNonZeroScalar(order *big.Int) (*big.Int, error) {
	upper := new(big.Int).Sub(order, big.NewInt(1))
	nonce, err := rand.Int(rand.Reader, upper)
	if err != nil {
		return nil, err
	}
	return nonce.Add(nonce, big.NewInt(1)), nil
}

// SchnorrProofVerify 使用公钥和会话验证承诺点及响应值。
func SchnorrProofVerify(
	alphaX, alphaY, response *big.Int,
	pubKey *ecdsa.PublicKey,
	session []byte,
) (bool, error) {
	if err := validatePublicKey(pubKey); err != nil {
		return false, err
	}
	if len(session) == 0 {
		return false, errors.New("Schnorr session is empty")
	}
	if alphaX == nil || alphaY == nil || response == nil {
		return false, errors.New("Schnorr proof is incomplete")
	}
	curve := pubKey.Curve
	if !curve.IsOnCurve(alphaX, alphaY) {
		return false, errors.New("Schnorr commitment is not on the public-key curve")
	}
	order := curve.Params().N
	if response.Sign() < 0 || response.Cmp(order) >= 0 {
		return false, errors.New("Schnorr response is outside the curve order")
	}
	challenge := challengeFor(
		session,
		order,
		pubKey.X,
		pubKey.Y,
		curve.Params().Gx,
		curve.Params().Gy,
		alphaX,
		alphaY,
	)
	responseX, responseY := curve.ScalarBaseMult(response.Bytes())
	challengeX, challengeY := curve.ScalarMult(pubKey.X, pubKey.Y, challenge.Bytes())
	expectedX, expectedY := curve.Add(alphaX, alphaY, challengeX, challengeY)
	if responseX == nil || responseY == nil || expectedX == nil || expectedY == nil {
		return false, nil
	}
	return responseX.Cmp(expectedX) == 0 && responseY.Cmp(expectedY) == 0, nil
}

// validatePrivateKey 校验私钥范围及其与公钥的一致性。
func validatePrivateKey(privateKey *ecdsa.PrivateKey) error {
	if privateKey == nil || privateKey.D == nil {
		return errors.New("Schnorr private key is nil or incomplete")
	}
	if err := validatePublicKey(&privateKey.PublicKey); err != nil {
		return err
	}
	order := privateKey.Curve.Params().N
	if privateKey.D.Sign() <= 0 || privateKey.D.Cmp(order) >= 0 {
		return errors.New("Schnorr private scalar is outside the curve order")
	}
	x, y := privateKey.Curve.ScalarBaseMult(privateKey.D.Bytes())
	if x.Cmp(privateKey.X) != 0 || y.Cmp(privateKey.Y) != 0 {
		return errors.New("Schnorr private and public keys do not match")
	}
	return nil
}

// validatePublicKey 校验公钥结构及曲线坐标。
func validatePublicKey(publicKey *ecdsa.PublicKey) error {
	if publicKey == nil || publicKey.Curve == nil || publicKey.X == nil || publicKey.Y == nil {
		return errors.New("Schnorr public key is nil or incomplete")
	}
	if publicKey.Curve.Params() == nil || publicKey.Curve.Params().N == nil || publicKey.Curve.Params().N.Cmp(big.NewInt(2)) < 0 {
		return errors.New("Schnorr public-key curve has incomplete parameters")
	}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return errors.New("Schnorr public key is not on its curve")
	}
	return nil
}

// challengeFor 按参考实现的带标签 SHA-512/256 转录计算挑战值。
func challengeFor(session []byte, order *big.Int, values ...*big.Int) *big.Int {
	tagHash := framedSHA512_256(session)
	hash := sha512.New512_256()
	_, _ = hash.Write(tagHash)
	_, _ = hash.Write(tagHash)
	_, _ = hash.Write(frameIntegers(values...))
	return new(big.Int).Mod(new(big.Int).SetBytes(hash.Sum(nil)), order)
}

// framedSHA512_256 使用数量、分隔符和长度对字节序列做无歧义哈希。
func framedSHA512_256(values ...[]byte) []byte {
	hash := sha512.New512_256()
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, uint64(len(values)))
	for _, value := range values {
		data = append(data, value...)
		data = append(data, '$')
		length := make([]byte, 8)
		binary.LittleEndian.PutUint64(length, uint64(len(value)))
		data = append(data, length...)
	}
	_, _ = hash.Write(data)
	return hash.Sum(nil)
}

// frameIntegers 将大整数序列编码为参考实现兼容的哈希输入。
func frameIntegers(values ...*big.Int) []byte {
	encoded := make([][]byte, len(values))
	for index, value := range values {
		if value != nil {
			encoded[index] = value.Bytes()
		}
	}
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, uint64(len(values)))
	for _, value := range encoded {
		data = append(data, value...)
		data = append(data, '$')
		length := make([]byte, 8)
		binary.LittleEndian.PutUint64(length, uint64(len(value)))
		data = append(data, length...)
	}
	return data
}
