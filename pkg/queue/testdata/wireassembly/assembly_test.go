package wireassembly

import (
	"context"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type captureStore struct {
	queue.Store
	task *queue.Task
}

func (s *captureStore) Enqueue(_ context.Context, task *queue.Task) error { s.task = task; return nil }

type tracingProvider struct{ tp trace.TracerProvider }

type testLogger struct{ log.Logger }

func (l testLogger) WithModule(string) log.Logger           { return l }
func (l testLogger) WithContext(context.Context) log.Logger { return l }
func (testLogger) Errorw(...any)                            {}

func (tracingProvider) Disabled() bool                         { return true }
func (p tracingProvider) TracerProvider() trace.TracerProvider { return p.tp }
func (p tracingProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return p.tp.Tracer(name, opts...)
}

func TestBindings(t *testing.T) {
	mp, cleanup, err := metrics.NewProvider(appinfo.New("queue-wire"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	mail, bot := &captureStore{}, &captureStore{}
	queues, err := initialize(queueStores{email: mail, bot: bot}, queue.Observability{Logger: testLogger{}, Metrics: mp, Tracing: tracingProvider{tp: noop.NewTracerProvider()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queues.Email.Publish(context.Background(), "mail"); err != nil {
		t.Fatal(err)
	}
	if _, err := queues.Bot.Publish(context.Background(), "bot"); err != nil {
		t.Fatal(err)
	}
	if mail.task == nil || bot.task == nil || string(mail.task.Payload) != `"mail"` || string(bot.task.Payload) != `"bot"` {
		t.Fatal("queue identities mixed")
	}
	var _ app.Runtime = (*emailQueue)(nil)
	var _ app.Runtime = (*botQueue)(nil)
}
