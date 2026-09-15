package log

import (
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestBuildCacheDoesNotPublishStaleSharedSettings(t *testing.T) {
	shared := &sharedState{}
	old := &customState{version: 1}
	current := &customState{version: 2}
	shared.custom.Store(current)
	l := &logger{shared: shared, config: &configState{
		output: &outputLogger{preparedOutput: &preparedOutput{output: loggerFunc(func(kratoslog.Level, ...any) error { return nil })}},
	}}
	if !l.buildCache(current) {
		t.Fatal("current settings were rejected")
	}
	if l.buildCache(old) {
		t.Fatal("stale settings were accepted")
	}
	if l.cache.customVersion != current.version {
		t.Fatalf("cache version = %d", l.cache.customVersion)
	}
}
