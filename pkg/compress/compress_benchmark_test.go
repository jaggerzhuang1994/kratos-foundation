package compress

import (
	"bytes"
	"runtime"
	"testing"
)

type benchmarkSize struct {
	name string
	size int
}

func BenchmarkCompress(b *testing.B) {
	for _, format := range compressionFormats() {
		for _, size := range compressionBenchmarkSizes() {
			data := benchmarkData(size.size)
			b.Run(format.name+"/"+size.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()

				var compressed []byte
				for i := 0; i < b.N; i++ {
					var err error
					compressed, err = format.compress(data)
					if err != nil {
						b.Fatalf("compress() error = %v", err)
					}
				}
				runtime.KeepAlive(compressed)
			})
		}
	}
}

func BenchmarkDecompress(b *testing.B) {
	benchmarkDecompress(b, false)
}

func BenchmarkDecompressLimit(b *testing.B) {
	benchmarkDecompress(b, true)
}

func benchmarkDecompress(b *testing.B, limited bool) {
	b.Helper()

	for _, format := range compressionFormats() {
		for _, size := range compressionBenchmarkSizes() {
			data := benchmarkData(size.size)
			compressed, err := format.compress(data)
			if err != nil {
				b.Fatalf("compress() error = %v", err)
			}

			b.Run(format.name+"/"+size.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()

				var decompressed []byte
				for i := 0; i < b.N; i++ {
					if limited {
						decompressed, err = format.decompressLimit(compressed, int64(len(data)))
					} else {
						decompressed, err = format.decompress(compressed)
					}
					if err != nil {
						b.Fatalf("decompress() error = %v", err)
					}
				}
				runtime.KeepAlive(decompressed)
			})
		}
	}
}

func compressionBenchmarkSizes() []benchmarkSize {
	return []benchmarkSize{
		{name: "1KiB", size: 1 << 10},
		{name: "1MiB", size: 1 << 20},
	}
}

func benchmarkData(size int) []byte {
	pattern := []byte("kratos-foundation benchmark payload\n")
	return bytes.Repeat(pattern, size/len(pattern)+1)[:size]
}
