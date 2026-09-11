package oss

import (
	"context"
	"io"

	"go.opentelemetry.io/otel/metric"
)

type metricsReader struct {
	io.Reader
	bucket    *metricsBucket
	operation string
}

func (r *metricsReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	// 即使同一次 Read 返回错误，n 个已读取字节仍计入；不使用声明的对象大小。
	if n > 0 {
		r.bucket.metrics.bytes.Add(context.Background(), int64(n), metric.WithAttributes(r.bucket.attrs(r.operation)...))
	}
	return n, err
}

type readerLength interface{ Len() int }

// wrapReader 保留 SDK 长度推断和回放需要的能力；nil 必须仍由驱动按原顺序校验。
func (b *metricsBucket) wrapReader(r io.Reader) io.Reader {
	if r == nil {
		return nil
	}
	counted := &metricsReader{Reader: r, bucket: b, operation: "put"}
	seeker, hasSeek := r.(io.Seeker)
	at, hasAt := r.(io.ReaderAt)
	length, hasLen := r.(readerLength)
	if hasAt {
		at = &metricsReaderAt{ReaderAt: at, reader: counted}
	}
	// 不转发 WriterTo，io.Copy 必须走计数后的 Read，不能绕过统计。
	switch {
	case hasSeek && hasAt && hasLen:
		return struct {
			io.Reader
			io.Seeker
			io.ReaderAt
			readerLength
		}{counted, seeker, at, length}
	case hasSeek && hasAt:
		return struct {
			io.Reader
			io.Seeker
			io.ReaderAt
		}{counted, seeker, at}
	case hasSeek && hasLen:
		return struct {
			io.Reader
			io.Seeker
			readerLength
		}{counted, seeker, length}
	case hasAt && hasLen:
		return struct {
			io.Reader
			io.ReaderAt
			readerLength
		}{counted, at, length}
	case hasSeek:
		return struct {
			io.Reader
			io.Seeker
		}{counted, seeker}
	case hasAt:
		return struct {
			io.Reader
			io.ReaderAt
		}{counted, at}
	case hasLen:
		return struct {
			io.Reader
			readerLength
		}{counted, length}
	default:
		return counted
	}
}

type metricsReaderAt struct {
	io.ReaderAt
	reader *metricsReader
}

func (r *metricsReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	if n > 0 {
		r.reader.bucket.metrics.bytes.Add(context.Background(), int64(n), metric.WithAttributes(r.reader.bucket.attrs(r.reader.operation)...))
	}
	return n, err
}
