package compress

import (
	"bytes"
	"errors"
	"math"
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
