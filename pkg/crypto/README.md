# crypto

`crypto` 汇集 Foundation 对外提供的密码学辅助包。调用方应先根据协议和威胁模型选择子包，不要把不同算法当作可以互换的通用加密工具。

```go
import (
    foundationaes "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/aes"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/ecc"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/password"
    foundationrsa "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/rsa"
    "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/schnorr"
)
```

## 子包选择

| 子包       | 用途                                             | 新系统建议                         |
|------------|--------------------------------------------------|------------------------------------|
| [`aes`](aes/README.md)      | 数据库字段的 AES-CBC 静态加密                    | 仅用于仓库声明的数据库泄漏威胁模型 |
| [`ecc`](ecc/README.md)      | 认证临时公钥和正文的 ECIES 加解密及 ECDSA 密钥编解码 | 仅在双方采用该 ECIES 格式时使用    |
| [`password`](password/README.md) | Argon2id 密码哈希、验证和升级判断                | 推荐                               |
| [`rsa`](rsa/README.md)      | 分块 RSA PKCS#1 v1.5 加解密及 PKCS#1 密钥编解码  | 仅兼容旧协议，不用于新设计         |
| [`schnorr`](schnorr/README.md)  | GG18 风格的 ECDSA 私钥知识证明                   | 仅用于明确采用该转录格式的协议     |

## AES-CBC

`aes.CBC` 实现 `aes.Cipher`：

```go
cipher := foundationaes.CBC{}
ciphertext, err := cipher.Encrypt(plain, key)
if err != nil {
    return err
}
plain, err = cipher.Decrypt(ciphertext, key)
if err != nil {
    return err
}
```

`Encrypt`/`Decrypt` 处理字节，`EncryptString`/`DecryptString` 处理字符串和标准 Base64 密文；四个方法共同组成 `Cipher` 接口。

- key 必须是 AES 支持的 16、24 或 32 字节。
- 二进制密文格式是 `IV || AES-CBC(PKCS#7(plaintext))`；字符串 API 使用标准 Base64。
- 每次加密都会生成随机 IV。
- CBC 不验证密文完整性或来源，不能替代 AEAD。密文经过不可信通道、可能被攻击者修改，或解密成败可被外部观察时，应使用 AES-GCM
  等认证加密方案。

## ECIES 与 EC 密钥

`ecc.GenerateKey` 生成 P-256 ECDSA 密钥。`EncodePrivateKey`/`ParsePrivateKey` 使用 EC PRIVATE KEY PEM，`EncodePublicKey`/
`ParsePublicKey` 使用 PKIX DER 封装的 EC PUBLIC KEY PEM。

```go
privateKey, publicKey, err := ecc.GenerateKey()
if err != nil {
    return err
}
ciphertext, err := ecc.Encrypt("secret", publicKey)
if err != nil {
    return err
}
plain, err := ecc.Decrypt(ciphertext, privateKey)
if err != nil {
    return err
}
```

ECIES 支持 P-256、P-384 和 P-521，密文格式为 `R || IV || ciphertext || HMAC`，其中 HMAC 覆盖 `R || IV || ciphertext`。
`EncryptToBase64StringByPubHex` 只接受 128
个十六进制字符表示的 P-256 `X || Y` 公钥，不包含 `04` 前缀。

| API                                                 | 用途                       |
|-----------------------------------------------------|----------------------------|
| `GenerateKey`                                       | 生成 P-256 ECDSA 密钥对    |
| `EncodePrivateKey` / `ParsePrivateKey`              | 编解码 EC 私钥 PEM         |
| `EncodePublicKey` / `ParsePublicKey`                | 编解码 EC 公钥 PEM         |
| `Encrypt` / `Decrypt`                               | 使用二进制 ECIES 密文      |
| `EncryptToBase64String` / `DecryptFromBase64String` | 使用标准 Base64 ECIES 密文 |
| `EncryptToBase64StringByPubHex`                     | 使用 P-256 `X \|\| Y` 十六进制公钥加密 |

此实现不应被理解为通用或可协商的 ECIES 协议。双方必须使用相同曲线、KDF、哈希和密文格式。当前认证临时公钥的格式与旧版只认证
`IV || ciphertext` 的密文不兼容，升级时必须迁移或重新加密旧数据。

## Argon2id 密码哈希

`password.HashPassword` 使用 RFC 9106 面向内存受限环境的参数，返回 PHC 字符串：

```go
encoded, err := password.HashPassword([]byte(userPassword))
if err != nil {
    return err
}

matched, err := password.VerifyPassword([]byte(candidate), encoded)
if err != nil {
    return fmt.Errorf("stored password hash is invalid: %w", err)
}
if !matched {
    return errInvalidCredentials
}
```

默认参数是 64 MiB 内存、3 次迭代、4 路并行、16 字节盐和 32 字节输出。`HashPasswordWithParams` 可显式调整成本；`NeedsRehash`
用于登录成功后判断是否应升级已有哈希。

`DefaultArgon2idParams` 返回默认的 `Argon2idParams`。该结构的 `MemoryKiB`、`Iterations`、`Parallelism`、`SaltLength` 和
`KeyLength` 分别控制内存、迭代次数、并行度、盐长度和输出长度；自定义参数应先在目标硬件上测量，并遵守服务的并发内存预算。

`VerifyPassword` 会先解析并限制 PHC 参数，再分配 Argon2 内存。返回 `false, nil` 表示密码不匹配；非 `nil` 错误表示存储的 PHC
字符串或参数无效。

## RSA 兼容 API

`rsa.Encrypt`/`rsa.Decrypt` 对数据分块后使用 RSA PKCS#1 v1.5。该方案只为旧数据或旧协议兼容保留：PKCS#1 v1.5 解密错误可能形成
padding oracle，而且直接分块 RSA 不适合加密任意业务数据。新协议应使用 RSA-OAEP 封装随机会话密钥，再用 AEAD 加密正文。

`GenerateKey` 生成指定位数的 RSA 密钥；生产密钥至少应使用 2048 位，并根据系统寿命和安全策略选择更高强度。
`EncodePrivateKey`/`ParsePrivateKey` 与 `EncodePublicKey`/`ParsePublicKey` 使用 PKCS#1 PEM。`EncryptToBase64String`/
`DecryptFromBase64String` 只对最终密文增加或移除标准 Base64 编码，不改变加密安全属性。

## Schnorr 知识证明

```go
alphaX, alphaY, response, err := schnorr.SchnorrProof(privateKey, session)
if err != nil {
    return err
}
valid, err := schnorr.SchnorrProofVerify(
  alphaX,
  alphaY,
  response,
  &privateKey.PublicKey,
  session,
)
if err != nil {
    return err
}
if !valid {
    return errInvalidProof
}
```

- `session` 必须非空，并且每次协议操作使用新的、不可混淆的会话值。
- 生成和验证必须使用完全相同的会话、曲线和公钥。
- 该证明采用仓库约定的 GG18 风格 SHA-512/256 转录，不是 BIP-340 Schnorr 签名，也不能替代数字签名。
- `false, nil` 表示结构有效但等式不成立；输入结构、曲线或标量非法时返回错误。

## 通用约束

- 密钥、密码、明文和证明随机数不得写入日志或错误元数据。
- 密钥应来自密码学安全随机源或专用密钥管理系统，不要把普通口令直接当作 AES/RSA/EC 密钥。
- 加密格式是持久化协议的一部分；修改算法、编码或参数前必须先设计版本字段和迁移策略。
