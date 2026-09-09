# compress

`compress` 为 Go 标准库中的 gzip、原始 DEFLATE 和 zlib 提供面向 `[]byte` 的内存压缩与解压辅助函数。它适合处理中小型、已经完整载入内存的数据；流式数据应直接使用标准库的
reader/writer。

## 引入

```go
import "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/compress"
```

## API

| 格式         | 压缩              | 解压                | 带输出上限的解压         |
|--------------|-------------------|---------------------|--------------------------|
| gzip         | `CompressGzip`    | `DecompressGzip`    | `DecompressGzipLimit`    |
| 原始 DEFLATE | `CompressDeflate` | `DecompressDeflate` | `DecompressDeflateLimit` |
| zlib         | `CompressZlib`    | `DecompressZlib`    | `DecompressZlibLimit`    |

压缩函数使用对应标准库实现的默认压缩级别。带 `Limit` 后缀的函数在解压结果超过 `maxBytes` 时返回 `ErrOutputTooLarge`；
`maxBytes <= 0` 表示不限制输出大小。

```go
compressed, err := compress.CompressGzip([]byte("hello"))
if err != nil {
    return err
}

plain, err := compress.DecompressGzipLimit(compressed, 1<<20)
if errors.Is(err, compress.ErrOutputTooLarge) {
    return fmt.Errorf("payload is too large: %w", err)
}
if err != nil {
    return err
}

```

## 使用约束

- 不可信压缩数据必须使用带 `Limit` 后缀的函数，避免压缩炸弹耗尽内存。
- `DecompressGzip`、`DecompressDeflate` 和 `DecompressZlib` 不限制输出大小，仅适用于大小已受其他边界控制的数据。
- DEFLATE API 处理的是不带 zlib 或 gzip 封装的原始 DEFLATE 流；三种格式不能混用。
- 返回的字节切片由调用方拥有，可以安全修改。
