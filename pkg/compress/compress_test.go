package compress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"sync"
	"testing"
)

type compressionFormat struct {
	name            string
	compress        func([]byte) ([]byte, error)
	decompress      func([]byte) ([]byte, error)
	decompressLimit func([]byte, int64) ([]byte, error)
}

func compressionFormats() []compressionFormat {
	return []compressionFormat{
		{name: "gzip", compress: CompressGzip, decompress: DecompressGzip, decompressLimit: DecompressGzipLimit},
		{name: "deflate", compress: CompressDeflate, decompress: DecompressDeflate, decompressLimit: DecompressDeflateLimit},
		{name: "zlib", compress: CompressZlib, decompress: DecompressZlib, decompressLimit: DecompressZlibLimit},
	}
}

func TestCompressionRoundTrip(t *testing.T) {
	t.Parallel()

	inputs := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "text", data: []byte("hello, kratos foundation")},
		{name: "binary", data: []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff}},
		{name: "repeated", data: bytes.Repeat([]byte("compressible payload"), 1024)},
	}

	for _, format := range compressionFormats() {
		format := format
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()

			for _, input := range inputs {
				input := input
				t.Run(input.name, func(t *testing.T) {
					t.Parallel()

					compressed, err := format.compress(input.data)
					if err != nil {
						t.Fatalf("compress() error = %v", err)
					}

					got, err := format.decompress(compressed)
					if err != nil {
						t.Fatalf("decompress() error = %v", err)
					}
					if !bytes.Equal(got, input.data) {
						t.Errorf("decompress() = %q, want %q", got, input.data)
					}
				})
			}
		})
	}
}

func TestDecompressLimit(t *testing.T) {
	t.Parallel()

	want := []byte("hello")
	limits := []struct {
		name    string
		limit   int64
		wantErr error
	}{
		{name: "negative means unlimited", limit: -1},
		{name: "zero means unlimited", limit: 0},
		{name: "exact limit", limit: int64(len(want))},
		{name: "output too large", limit: int64(len(want) - 1), wantErr: ErrOutputTooLarge},
	}

	for _, format := range compressionFormats() {
		format := format
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()

			compressed, err := format.compress(want)
			if err != nil {
				t.Fatalf("compress() error = %v", err)
			}

			for _, limit := range limits {
				limit := limit
				t.Run(limit.name, func(t *testing.T) {
					t.Parallel()

					got, err := format.decompressLimit(compressed, limit.limit)
					if !errors.Is(err, limit.wantErr) {
						t.Fatalf("decompressLimit() error = %v, want %v", err, limit.wantErr)
					}
					if limit.wantErr == nil && !bytes.Equal(got, want) {
						t.Errorf("decompressLimit() = %q, want %q", got, want)
					}
				})
			}
		})
	}
}

func TestDecompressLimitMaxInt64(t *testing.T) {
	t.Parallel()

	for _, format := range compressionFormats() {
		format := format
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()

			want := []byte("hello")
			compressed, err := format.compress(want)
			if err != nil {
				t.Fatalf("compress() error = %v", err)
			}

			got, err := format.decompressLimit(compressed, math.MaxInt64)
			if err != nil {
				t.Fatalf("decompressLimit() error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("decompressLimit() = %q, want %q", got, want)
			}
		})
	}
}

func TestDecompressRejectsTruncatedData(t *testing.T) {
	t.Parallel()

	for _, format := range compressionFormats() {
		format := format
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()

			want := bytes.Repeat([]byte("payload"), 32)
			compressed, err := format.compress(want)
			if err != nil {
				t.Fatalf("compress() error = %v", err)
			}
			truncated := compressed[:len(compressed)-1]

			if _, err := format.decompress(truncated); err == nil {
				t.Fatal("decompress() error = nil, want malformed stream error")
			}
		})
	}
}

func TestDecompressLimitRejectsTruncatedDataAtExactLimit(t *testing.T) {
	t.Parallel()

	for _, format := range compressionFormats() {
		format := format
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()

			want := bytes.Repeat([]byte("payload"), 32)
			compressed, err := format.compress(want)
			if err != nil {
				t.Fatalf("compress() error = %v", err)
			}
			truncated := compressed[:len(compressed)-1]

			if _, err := format.decompressLimit(truncated, int64(len(want))); err == nil {
				t.Fatal("decompressLimit() error = nil, want malformed stream error")
			}
		})
	}
}

func TestCompressionMatchesFreshWriterAndOwnsOutput(t *testing.T) {
	factories := []func(io.Writer) io.WriteCloser{
		func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) },
		func(w io.Writer) io.WriteCloser {
			v, err := flate.NewWriter(w, flate.DefaultCompression)
			if err != nil {
				t.Fatal(err)
			}
			return v
		},
		func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) },
	}
	for i, format := range compressionFormats() {
		t.Run(format.name, func(t *testing.T) {
			t.Parallel()
			var retained [][]byte
			var copies [][]byte
			for _, size := range []int{0, 1024, 1 << 20, 13, 1024} {
				input := bytes.Repeat([]byte{byte(size % 251)}, size)
				var expected bytes.Buffer
				w := factories[i](&expected)
				if _, err := w.Write(input); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				got, err := format.compress(input)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, expected.Bytes()) {
					t.Fatalf("size %d differs from fresh writer", size)
				}
				retained = append(retained, got)
				copies = append(copies, bytes.Clone(got))
			}
			for i := range retained {
				if !bytes.Equal(retained[i], copies[i]) {
					t.Fatal("later call changed previous output")
				}
			}
		})
	}
}

func BenchmarkCompressionReuse(b *testing.B) {
	for _, format := range compressionFormats() {
		for _, size := range []int{1024, 65536} {
			data := make([]byte, size)
			r := rand.New(rand.NewPCG(1, 2))
			for i := range data {
				data[i] = byte(r.Uint32())
			}
			b.Run(fmt.Sprintf("%s/random%d", format.name, size), func(b *testing.B) {
				for b.Loop() {
					if _, err := format.compress(data); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		data := bytes.Repeat([]byte("a"), 1024)
		b.Run(format.name+"/parallel1K", func(b *testing.B) {
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := format.compress(data); err != nil {
						b.Error(err)
					}
				}
			})
		})
	}
}

type failedCompressionWriter struct{ writeErr, closeErr error }

func (w *failedCompressionWriter) Write(p []byte) (int, error) { return len(p), w.writeErr }
func (w *failedCompressionWriter) Close() error                { return w.closeErr }
func (w *failedCompressionWriter) Reset(io.Writer) {
	panic("failed writer must not be reset and pooled")
}

func TestCompressionDiscardsFailedWriters(t *testing.T) {
	failure := errors.New("compression failure")
	for _, stage := range []string{"create", "write", "close"} {
		t.Run(stage, func(t *testing.T) {
			var pool sync.Pool
			create := func(io.Writer) (resetWriter, error) {
				switch stage {
				case "create":
					return nil, failure
				case "write":
					return &failedCompressionWriter{writeErr: failure}, nil
				default:
					return &failedCompressionWriter{closeErr: failure}, nil
				}
			}
			if _, err := compress([]byte("input"), &pool, create); !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
			if pool.Get() != nil {
				t.Fatal("failed writer retained in pool")
			}
		})
	}
}
