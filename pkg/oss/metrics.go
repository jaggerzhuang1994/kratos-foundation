package oss

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WithMetrics 为 Manager 返回的 bucket 记录指标，cleanup 仍由原 Manager 的构造调用方负责。
// 必须注入应用导出端使用的同一 Provider；不要重复包装同一个 Manager。
func WithMetrics(manager Manager, provider metrics.Provider) (Manager, error) {
	meter := provider.Meter("kratos-foundation/oss")
	m := &metricsManager{Manager: manager}
	var err error
	if m.requests, err = meter.Int64Counter("oss_requests_total"); err != nil {
		return nil, fmt.Errorf("create OSS request metric: %w", err)
	}
	if m.duration, err = meter.Float64Histogram("oss_request_duration_seconds", metric.WithUnit("s")); err != nil {
		return nil, fmt.Errorf("create OSS duration metric: %w", err)
	}
	if m.bytes, err = meter.Int64Counter("oss_transferred_bytes_total", metric.WithUnit("By")); err != nil {
		return nil, fmt.Errorf("create OSS bytes metric: %w", err)
	}
	if m.streams, err = meter.Int64Counter("oss_streams_total"); err != nil {
		return nil, fmt.Errorf("create OSS stream metric: %w", err)
	}
	if m.streamDuration, err = meter.Float64Histogram("oss_stream_duration_seconds", metric.WithUnit("s")); err != nil {
		return nil, fmt.Errorf("create OSS stream duration metric: %w", err)
	}
	return m, nil
}

type metricsManager struct {
	Manager
	requests       metric.Int64Counter
	duration       metric.Float64Histogram
	bytes          metric.Int64Counter
	streams        metric.Int64Counter
	streamDuration metric.Float64Histogram
}

func (m *metricsManager) Bucket(name string) (Bucket, error) {
	bucket, err := m.Manager.Bucket(name)
	if err != nil {
		return nil, err
	}
	b := &metricsBucket{Bucket: bucket, metrics: m, name: strings.TrimSpace(name)}
	// 只暴露驱动原本提供的可选能力，避免类型断言误报支持。
	l, hasL := bucket.(Lister)
	c, hasC := bucket.(Copier)
	u, hasU := bucket.(URLResolver)
	if hasL {
		l = &metricsLister{b, l}
	}
	if hasC {
		c = &metricsCopier{b, c}
	}
	switch {
	case hasL && hasC && hasU:
		return struct {
			Bucket
			Lister
			Copier
			URLResolver
		}{b, l, c, u}, nil
	case hasL && hasC:
		return struct {
			Bucket
			Lister
			Copier
		}{b, l, c}, nil
	case hasL && hasU:
		return struct {
			Bucket
			Lister
			URLResolver
		}{b, l, u}, nil
	case hasC && hasU:
		return struct {
			Bucket
			Copier
			URLResolver
		}{b, c, u}, nil
	case hasL:
		return struct {
			Bucket
			Lister
		}{b, l}, nil
	case hasC:
		return struct {
			Bucket
			Copier
		}{b, c}, nil
	case hasU:
		return struct {
			Bucket
			URLResolver
		}{b, u}, nil
	default:
		return b, nil
	}
}

type metricsBucket struct {
	Bucket
	metrics *metricsManager
	name    string
}

func (b *metricsBucket) attrs(operation string) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("bucket", b.name), attribute.String("operation", operation)}
}
func (b *metricsBucket) record(ctx context.Context, operation string, start time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(append(b.attrs(operation), attribute.String("result", result))...)
	b.metrics.requests.Add(ctx, 1, attrs)
	b.metrics.duration.Record(ctx, time.Since(start).Seconds(), attrs)
}
func (b *metricsBucket) PutObject(ctx context.Context, key string, r io.Reader, o PutOptions) (ObjectInfo, error) {
	start := time.Now()
	info, err := b.Bucket.PutObject(ctx, key, b.wrapReader(r), o)
	b.record(ctx, "put", start, err)
	return info, err
}
func (b *metricsBucket) GetObject(ctx context.Context, key string, o GetOptions) (*Object, error) {
	start := time.Now()
	object, err := b.Bucket.GetObject(ctx, key, o)
	b.record(ctx, "get", start, err)
	if err == nil && object != nil && object.Body != nil {
		// 复制对象外壳，避免替换驱动可能持有的 Object.Body；底层流所有权不变。
		result := *object
		result.Body = &metricsBody{body: object.Body, reader: metricsReader{Reader: object.Body, bucket: b, operation: "get"}, start: start}
		return &result, nil
	}
	return object, err
}
func (b *metricsBucket) DeleteObject(ctx context.Context, key string) error {
	start := time.Now()
	err := b.Bucket.DeleteObject(ctx, key)
	b.record(ctx, "delete", start, err)
	return err
}
func (b *metricsBucket) StatObject(ctx context.Context, key string) (ObjectInfo, error) {
	start := time.Now()
	info, err := b.Bucket.StatObject(ctx, key)
	b.record(ctx, "stat", start, err)
	return info, err
}
func (b *metricsBucket) ObjectExists(ctx context.Context, key string) (bool, error) {
	start := time.Now()
	exists, err := b.Bucket.ObjectExists(ctx, key)
	b.record(ctx, "exists", start, err)
	return exists, err
}

type metricsLister struct {
	bucket *metricsBucket
	lister Lister
}

func (l *metricsLister) ListObjects(ctx context.Context, o ListOptions) (ListResult, error) {
	start := time.Now()
	result, err := l.lister.ListObjects(ctx, o)
	l.bucket.record(ctx, "list", start, err)
	return result, err
}

type metricsCopier struct {
	bucket *metricsBucket
	copier Copier
}

func (c *metricsCopier) CopyObject(ctx context.Context, source, target string, o CopyOptions) (ObjectInfo, error) {
	start := time.Now()
	result, err := c.copier.CopyObject(ctx, source, target, o)
	c.bucket.record(ctx, "copy", start, err)
	return result, err
}

type metricsBody struct {
	body                io.ReadCloser
	reader              metricsReader
	start               time.Time
	eof, failed, closed bool
}

func (b *metricsBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.failed = true
	}
	return n, err
}
func (b *metricsBody) Close() error {
	err := b.body.Close()
	if b.closed {
		return err
	}
	b.closed = true
	// Close 才结束统计，保证 EOF 后的关闭失败也可见；首个 Close 决定最终结果。
	result := "closed_early"
	switch {
	case err != nil:
		result = "close_error"
	case b.failed:
		result = "read_error"
	case b.eof:
		result = "success"
	}
	attrs := metric.WithAttributes(append(b.reader.bucket.attrs("get"), attribute.String("result", result))...)
	b.reader.bucket.metrics.streams.Add(context.Background(), 1, attrs)
	b.reader.bucket.metrics.streamDuration.Record(context.Background(), time.Since(b.start).Seconds(), attrs)
	return err
}
