package gormscope

import (
	"runtime"
	"testing"
	"time"
)

func BenchmarkGetSortKey(b *testing.B) {
	keys := SortKeys{
		TimeDescSortKey("created_at"),
		NumberAscSortKey("id"),
		StringAscSortKey("name"),
	}
	model := struct {
		ID        uint64
		Name      string
		CreatedAt time.Time
	}{ID: 42, Name: "benchmark", CreatedAt: time.Unix(1700000000, 123456789)}
	b.ReportAllocs()
	b.ResetTimer()

	var cursor string
	for i := 0; i < b.N; i++ {
		var err error
		cursor, err = keys.GetSortKey(model, ":")
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(cursor)
}
