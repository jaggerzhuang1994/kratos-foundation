package oss

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

func TestMetricsUploadReaderCapabilities(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(string(rune('0'+mask)), func(t *testing.T) {
			p, cleanup, err := metrics.NewProvider(metricsAppInfo{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			source := strings.NewReader("abc")
			var input io.Reader = struct{ io.Reader }{source}
			switch mask {
			case 1:
				input = struct {
					io.Reader
					io.Seeker
				}{source, source}
			case 2:
				input = struct {
					io.Reader
					io.ReaderAt
				}{source, source}
			case 3:
				input = struct {
					io.Reader
					io.Seeker
					io.ReaderAt
				}{source, source, source}
			case 4:
				input = struct {
					io.Reader
					readerLength
				}{source, source}
			case 5:
				input = struct {
					io.Reader
					io.Seeker
					readerLength
				}{source, source, source}
			case 6:
				input = struct {
					io.Reader
					io.ReaderAt
					readerLength
				}{source, source, source}
			case 7:
				input = source // 同时含 WriterTo，包装后不能让 Copy 绕过统计。
			}
			wantBytes := float64(3)
			raw := &metricsTestBucket{put: func(r io.Reader) error {
				seeker, hasSeek := r.(io.Seeker)
				at, hasAt := r.(io.ReaderAt)
				length, hasLen := r.(readerLength)
				if hasSeek != (mask&1 != 0) || hasAt != (mask&2 != 0) || hasLen != (mask&4 != 0) {
					t.Fatal("upload capability mismatch")
				}
				if _, ok := r.(io.WriterTo); ok {
					t.Fatal("WriterTo bypasses counting")
				}
				if hasLen && length.Len() != 3 {
					t.Fatal("wrong length")
				}
				if _, err := io.Copy(io.Discard, r); err != nil {
					return err
				}
				if hasSeek {
					if _, err := seeker.Seek(0, io.SeekStart); err != nil {
						return err
					}
					if _, err := io.Copy(io.Discard, r); err != nil {
						return err
					}
					wantBytes += 3
				}
				if hasAt {
					// 返回非零 n 和 EOF 时，ReaderAt 仍应计入已返回的字节。
					n, err := at.ReadAt(make([]byte, 5), 0)
					if n != 3 || err != io.EOF {
						t.Fatalf("readAt=%d,%v", n, err)
					}
					wantBytes += 3
				}
				return nil
			}}
			m, err := WithMetrics(metricsTestManager{raw}, p)
			if err != nil {
				t.Fatal(err)
			}
			b, err := m.Bucket("assets")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.PutObject(context.Background(), "key", input, PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := metricValue(t, p, "oss_transferred_bytes_total", map[string]string{"operation": "put"}); got != wantBytes {
				t.Fatalf("bytes=%v want=%v", got, wantBytes)
			}
		})
	}
}

func TestMetricsUploadPreservesNil(t *testing.T) {
	p, cleanup, err := metrics.NewProvider(metricsAppInfo{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	nilErr := errors.New("object body is nil")
	raw := &metricsTestBucket{put: func(r io.Reader) error {
		if r != nil {
			t.Fatal("nil body hidden by wrapper")
		}
		return nilErr
	}}
	m, err := WithMetrics(metricsTestManager{raw}, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Bucket("assets")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.PutObject(context.Background(), "key", nil, PutOptions{}); !errors.Is(err, nilErr) {
		t.Fatalf("error=%v", err)
	}
	if got := metricValue(t, p, "oss_requests_total", map[string]string{"operation": "put", "result": "error"}); got != 1 {
		t.Fatalf("errors=%v", got)
	}
}
