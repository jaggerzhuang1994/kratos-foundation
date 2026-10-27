package observability_test

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// queue 与 kafka 的 Observability 是同一类型，同一个值可直接互传。
func TestComponentObservabilityAliases(t *testing.T) {
	shared := observability.New(nil, nil, nil)
	toQueue := func(o queue.Observability) queue.Observability { return o }
	toKafka := func(o kafka.Observability) kafka.Observability { return o }
	if toKafka(toQueue(shared)) != shared || queue.NewObservability(nil, nil, nil) != shared {
		t.Fatal("observability aliases diverged")
	}
}
