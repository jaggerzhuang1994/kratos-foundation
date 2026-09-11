package oss

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

type metricsAppInfo struct{}

func (metricsAppInfo) ID() string                  { return "test" }
func (metricsAppInfo) Name() string                { return "oss-test" }
func (metricsAppInfo) Version() string             { return "test" }
func (metricsAppInfo) Metadata() map[string]string { return nil }

type metricsTestManager struct{ bucket Bucket }

func (m metricsTestManager) Bucket(string) (Bucket, error) { return m.bucket, nil }
func (m metricsTestManager) BucketNames() []string         { return []string{"assets"} }

type metricsTestBucket struct {
	fakeBucket
	body io.ReadCloser
	put  func(io.Reader) error
	err  error
}

func (b *metricsTestBucket) PutObject(_ context.Context, _ string, r io.Reader, _ PutOptions) (ObjectInfo, error) {
	if b.err != nil {
		return ObjectInfo{}, b.err
	}
	if b.put != nil {
		return ObjectInfo{}, b.put(r)
	}
	_, err := io.Copy(io.Discard, r)
	return ObjectInfo{}, err
}
func (b *metricsTestBucket) GetObject(context.Context, string, GetOptions) (*Object, error) {
	if b.err != nil {
		return nil, b.err
	}
	return &Object{ObjectInfo: ObjectInfo{Size: 99999}, Body: b.body}, nil
}

type metricsTestBody struct {
	io.Reader
	closeErr error
}

func (b metricsTestBody) Close() error { return b.closeErr }

type metricsFailReader struct{}

func (metricsFailReader) Read(p []byte) (int, error) { p[0] = 'x'; return 1, io.ErrUnexpectedEOF }

func metricValue(t *testing.T, p metrics.Provider, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := p.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.Metric {
			actual := map[string]string{}
			for _, label := range sample.Label {
				actual[label.GetName()] = label.GetValue()
			}
			match := true
			for key, value := range labels {
				if actual[key] != value {
					match = false
				}
			}
			if match {
				if sample.Histogram != nil {
					return float64(sample.Histogram.GetSampleCount())
				}
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}
func TestWithMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		reader       io.Reader
		closeErr     error
		early        bool
		bytes        float64
	}{
		{name: "eof", result: "success", reader: strings.NewReader("hello"), bytes: 5},
		{name: "read failure", result: "read_error", reader: metricsFailReader{}, bytes: 1},
		{name: "early close", result: "closed_early", reader: strings.NewReader("hello"), early: true},
		{name: "close failure", result: "close_error", reader: strings.NewReader("hello"), closeErr: io.ErrClosedPipe, bytes: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, cleanup, err := metrics.NewProvider(metricsAppInfo{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			raw := &metricsTestBucket{body: metricsTestBody{Reader: tc.reader, closeErr: tc.closeErr}}
			m, err := WithMetrics(metricsTestManager{raw}, p)
			if err != nil {
				t.Fatal(err)
			}
			b, err := m.Bucket(" assets ")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			o, err := b.GetObject(ctx, "sensitive-key", GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.early {
				_, readErr := io.Copy(io.Discard, o.Body)
				if (tc.result == "read_error") != errors.Is(readErr, io.ErrUnexpectedEOF) {
					t.Fatalf("read error=%v", readErr)
				}
			}
			if got := metricValue(t, p, "oss_streams_total", map[string]string{"operation": "get"}); got != 0 {
				t.Fatalf("unfinished streams=%v", got)
			}
			if err := o.Body.Close(); !errors.Is(err, tc.closeErr) {
				t.Fatalf("close error=%v", err)
			}
			_ = o.Body.Close()
			labels := map[string]string{"bucket": "assets", "operation": "get", "result": tc.result}
			if got := metricValue(t, p, "oss_streams_total", labels); got != 1 {
				t.Fatalf("streams=%v", got)
			}
			if got := metricValue(t, p, "oss_stream_duration_seconds", labels); got != 1 {
				t.Fatalf("stream durations=%v", got)
			}
			delete(labels, "result")
			if got := metricValue(t, p, "oss_transferred_bytes_total", labels); got != tc.bytes {
				t.Fatalf("bytes=%v want=%v", got, tc.bytes)
			}
			labels["result"] = "success"
			if got := metricValue(t, p, "oss_requests_total", labels); got != 1 {
				t.Fatalf("requests=%v", got)
			}
			if got := metricValue(t, p, "oss_request_duration_seconds", labels); got != 1 {
				t.Fatalf("durations=%v", got)
			}
			size := int64(999)
			if _, err := b.PutObject(ctx, "key", strings.NewReader("abc"), PutOptions{Size: &size}); err != nil {
				t.Fatal(err)
			}
			if _, err := b.PutObject(ctx, "key", metricsFailReader{}, PutOptions{}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			if got := metricValue(t, p, "oss_transferred_bytes_total", map[string]string{"operation": "put"}); got != 4 {
				t.Fatalf("put bytes=%v", got)
			}
			raw.err = io.ErrClosedPipe
			if _, err := b.GetObject(ctx, "key", GetOptions{}); !errors.Is(err, raw.err) {
				t.Fatal(err)
			}
			if got := metricValue(t, p, "oss_requests_total", map[string]string{"operation": "get", "result": "error"}); got != 1 {
				t.Fatalf("errors=%v", got)
			}
		})
	}
}

type metricsOptional struct{}

func (metricsOptional) ListObjects(context.Context, ListOptions) (ListResult, error) {
	return ListResult{NextCursor: "next"}, nil
}
func (metricsOptional) CopyObject(context.Context, string, string, CopyOptions) (ObjectInfo, error) {
	return ObjectInfo{Size: 7}, nil
}
func (metricsOptional) ObjectURL(string) (string, error) { return "https://example.com", nil }
func TestWithMetricsCapabilities(t *testing.T) {
	p, cleanup, err := metrics.NewProvider(metricsAppInfo{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	base := &metricsTestBucket{}
	optional := metricsOptional{}
	buckets := []Bucket{base,
		struct {
			Bucket
			Lister
		}{base, optional}, struct {
			Bucket
			Copier
		}{base, optional}, struct {
			Bucket
			URLResolver
		}{base, optional},
		struct {
			Bucket
			Lister
			Copier
		}{base, optional, optional}, struct {
			Bucket
			Lister
			URLResolver
		}{base, optional, optional}, struct {
			Bucket
			Copier
			URLResolver
		}{base, optional, optional},
		struct {
			Bucket
			Lister
			Copier
			URLResolver
		}{base, optional, optional, optional}}
	for _, raw := range buckets {
		m, err := WithMetrics(metricsTestManager{raw}, p)
		if err != nil {
			t.Fatal(err)
		}
		b, err := m.Bucket("assets")
		if err != nil {
			t.Fatal(err)
		}
		l, hasL := b.(Lister)
		_, wantL := raw.(Lister)
		c, hasC := b.(Copier)
		_, wantC := raw.(Copier)
		u, hasU := b.(URLResolver)
		_, wantU := raw.(URLResolver)
		if hasL != wantL || hasC != wantC || hasU != wantU {
			t.Fatal("capability mismatch")
		}
		if hasL {
			r, e := l.ListObjects(context.Background(), ListOptions{})
			if e != nil || r.NextCursor != "next" {
				t.Fatal(r, e)
			}
		}
		if hasC {
			r, e := c.CopyObject(context.Background(), "a", "b", CopyOptions{})
			if e != nil || r.Size != 7 {
				t.Fatal(r, e)
			}
		}
		if hasU {
			r, e := u.ObjectURL("a")
			if e != nil || r != "https://example.com" {
				t.Fatal(r, e)
			}
		}
		if e := b.DeleteObject(context.Background(), "a"); e != nil {
			t.Fatal(e)
		}
		if _, e := b.StatObject(context.Background(), "a"); e != nil {
			t.Fatal(e)
		}
		if _, e := b.ObjectExists(context.Background(), "a"); e != nil {
			t.Fatal(e)
		}
	}
	for _, op := range []string{"list", "copy", "delete", "stat", "exists"} {
		if metricValue(t, p, "oss_requests_total", map[string]string{"operation": op}) == 0 {
			t.Fatalf("missing %s", op)
		}
	}
}
