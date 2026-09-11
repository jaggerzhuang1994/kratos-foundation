package log

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

func TestAutomaticCallerPreservesOrdinaryFunctions(t *testing.T) {
	_, _, line, _ := runtime.Caller(0)
	got := caller(1)(context.Background())
	if want := fmt.Sprintf("log/caller_test.go:%d", line+1); got != want {
		t.Fatalf("caller=%v, want %s", got, want)
	}
}

func TestCallerWrapperMatchesOnlyKnownForwarders(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka.(*loggerAdapter).Log", true},
		{"github.com/twmb/franz-go/pkg/kgo.(*wrappedLogger).Log", true},
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job.(*cronLogger).Error", true},
		{"github.com/go-kratos/kratos/v2/log.(*Helper).Errorf", true},
		{"github.com/go-kratos/kratos/v2/log.Info", true},
		{"github.com/go-kratos/kratos/v2/log.businessEvent", false},
		{"example.com/business/log.Info", false},
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job.warnConcurrent", false},
		{"github.com/go-kratos/kratos/v2/middleware/logging.Server.func1.1", false},
		{"github.com/twmb/franz-go/pkg/kgo.(*Client).Produce", false},
	} {
		if got := callerWrapper(tc.name); got != tc.want {
			t.Errorf("%s=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCallerBeyondAvailableFramesIsUnknown(t *testing.T) {
	if got := caller(1000)(context.Background()); got != "unknown" {
		t.Fatalf("caller=%v, want unknown", got)
	}
}
