package oss

import (
	"context"
	"io"

	"go.opentelemetry.io/otel/metric"
)

type metricsReader struct {
	// Reader 实际读取数据的底层流。
	io.Reader
	// bucket 提供传输字节指标和标签的 bucket。
	bucket *metricsBucket
	// operation 用于区分上传与下载的操作标签。
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
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// Seeker 保留底层流的位置调整能力。
			io.Seeker
			// ReaderAt 保留并统计按偏移读取的能力。
			io.ReaderAt
			// readerLength 保留底层流长度查询，供 SDK 推断长度。
			readerLength
		}{counted, seeker, at, length}
	case hasSeek && hasAt:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// Seeker 保留底层流的位置调整能力。
			io.Seeker
			// ReaderAt 保留并统计按偏移读取的能力。
			io.ReaderAt
		}{counted, seeker, at}
	case hasSeek && hasLen:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// Seeker 保留底层流的位置调整能力。
			io.Seeker
			// readerLength 保留底层流长度查询，供 SDK 推断长度。
			readerLength
		}{counted, seeker, length}
	case hasAt && hasLen:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// ReaderAt 保留并统计按偏移读取的能力。
			io.ReaderAt
			// readerLength 保留底层流长度查询，供 SDK 推断长度。
			readerLength
		}{counted, at, length}
	case hasSeek:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// Seeker 保留底层流的位置调整能力。
			io.Seeker
		}{counted, seeker}
	case hasAt:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// ReaderAt 保留并统计按偏移读取的能力。
			io.ReaderAt
		}{counted, at}
	case hasLen:
		return struct {
			// Reader 统计实际读取字节的顺序读取能力。
			io.Reader
			// readerLength 保留底层流长度查询，供 SDK 推断长度。
			readerLength
		}{counted, length}
	default:
		return counted
	}
}

type metricsReaderAt struct {
	// ReaderAt 支持按偏移读取的底层能力。
	io.ReaderAt
	// reader 复用所属读流的指标和操作标签。
	reader *metricsReader
}

func (r *metricsReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	if n > 0 {
		r.reader.bucket.metrics.bytes.Add(context.Background(), int64(n), metric.WithAttributes(r.reader.bucket.attrs(r.reader.operation)...))
	}
	return n, err
}
