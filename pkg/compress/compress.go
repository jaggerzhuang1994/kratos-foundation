// Package compress provides in-memory helpers for the compression formats in
// the Go standard library.
package compress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// ErrOutputTooLarge is returned when decompression exceeds the configured
// maximum output size.
var ErrOutputTooLarge = errors.New("decompressed output exceeds limit")

// CompressGzip 使用默认级别压缩为 gzip 数据。
func CompressGzip(data []byte) ([]byte, error) {
	return compress(data, func(writer io.Writer) (io.WriteCloser, error) {
		return gzip.NewWriter(writer), nil
	})
}

// DecompressGzip 解压 gzip 数据且不限制输出大小；不可信输入应使用 DecompressGzipLimit。
func DecompressGzip(data []byte) ([]byte, error) {
	return DecompressGzipLimit(data, 0)
}

// DecompressGzipLimit 解压 gzip 数据，并拒绝超过 maxBytes 的输出；非正数表示不限制。
func DecompressGzipLimit(data []byte, maxBytes int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	return readCompressed(reader, maxBytes)
}

// CompressDeflate 将数据压缩为原始 DEFLATE 流。
func CompressDeflate(data []byte) ([]byte, error) {
	return compress(data, func(writer io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(writer, flate.DefaultCompression)
	})
}

// DecompressDeflate 解压原始 DEFLATE 流且不限制输出大小。
func DecompressDeflate(data []byte) ([]byte, error) {
	return DecompressDeflateLimit(data, 0)
}

// DecompressDeflateLimit 解压原始 DEFLATE 流，并拒绝超过 maxBytes 的输出。
func DecompressDeflateLimit(data []byte, maxBytes int64) ([]byte, error) {
	return readCompressed(flate.NewReader(bytes.NewReader(data)), maxBytes)
}

// CompressZlib 将数据压缩为 zlib 流。
func CompressZlib(data []byte) ([]byte, error) {
	return compress(data, func(writer io.Writer) (io.WriteCloser, error) {
		return zlib.NewWriter(writer), nil
	})
}

// DecompressZlib 解压 zlib 数据且不限制输出大小。
func DecompressZlib(data []byte) ([]byte, error) {
	return DecompressZlibLimit(data, 0)
}

// DecompressZlibLimit 解压 zlib 数据，并拒绝超过 maxBytes 的输出。
func DecompressZlibLimit(data []byte, maxBytes int64) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create zlib reader: %w", err)
	}
	return readCompressed(reader, maxBytes)
}

// compress 复用压缩器的写入和关闭错误处理。
func compress(data []byte, newWriter func(io.Writer) (io.WriteCloser, error)) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := newWriter(&buffer)
	if err != nil {
		return nil, err
	}
	if _, err = writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("write compressed data: %w", err)
	}
	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("finish compressed data: %w", err)
	}
	return buffer.Bytes(), nil
}

// readCompressed 读取解压流并实施可选的输出上限。
func readCompressed(reader io.ReadCloser, maxBytes int64) ([]byte, error) {
	defer func(reader io.ReadCloser) {
		_ = reader.Close()
	}(reader)
	if maxBytes <= 0 {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read decompressed data: %w", err)
		}
		return data, nil
	}

	limited := &io.LimitedReader{R: reader, N: maxBytes}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read decompressed data: %w", err)
	}
	if limited.N > 0 {
		return data, nil
	}

	// 额度恰好耗尽时额外探测一个字节，区分精确命中上限与输出超限，并避免 maxBytes+1 溢出。
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read decompressed data: %w", err)
	}
	if n > 0 {
		return nil, ErrOutputTooLarge
	}
	return data, nil
}
