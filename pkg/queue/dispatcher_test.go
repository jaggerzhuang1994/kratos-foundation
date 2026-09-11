package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type testLog struct {
	log.Logger
	mu     sync.Mutex
	events []string
}

func (l *testLog) WithContext(context.Context) log.Logger { return l }
func (l *testLog) Debugw(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, fmt.Sprint(args...))
}
func (l *testLog) Warnw(args ...any)  { l.Debugw(args...) }
func (l *testLog) Errorw(args ...any) { l.Debugw(args...) }

type tracingProvider struct{ tp trace.TracerProvider }

func (t tracingProvider) Disabled() bool                       { return false }
func (t tracingProvider) TracerProvider() trace.TracerProvider { return t.tp }
func (t tracingProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return t.tp.Tracer(name, opts...)
}

func testObservability(t *testing.T) (Observability, *tracetest.InMemoryExporter) {
	t.Helper()
	mp, cleanup, err := metrics.NewProvider(appinfo.New("queue-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	exporter := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return Observability{Logger: &testLog{}, Metrics: mp, Tracing: tracingProvider{tp}}, exporter
}

// storeStub 仅替换有外部副作用的存储操作，测试按场景提供实际调用的方法。
type storeStub struct {
	Store
	enqueue func(context.Context, *Task) error
	reserve func(context.Context, time.Time, time.Duration) (*Reservation, error)
	ack     func(context.Context, *Reservation) error
	release func(context.Context, *Reservation, time.Time) error
	fail    func(context.Context, *Reservation, string, time.Time) error
}

func (s *storeStub) Enqueue(c context.Context, t *Task) error { return s.enqueue(c, t) }
func (s *storeStub) Reserve(c context.Context, n time.Time, l time.Duration) (*Reservation, error) {
	return s.reserve(c, n, l)
}
func (s *storeStub) Ack(c context.Context, r *Reservation) error { return s.ack(c, r) }
func (s *storeStub) Release(c context.Context, r *Reservation, n time.Time) error {
	return s.release(c, r, n)
}
func (s *storeStub) Fail(c context.Context, r *Reservation, v string, n time.Time) error {
	return s.fail(c, r, v, n)
}

func TestDispatchOwnsSnapshotAndPropagatesTrace(t *testing.T) {
	obs, exporter := testObservability(t)
	var stored *Task
	store := &storeStub{enqueue: func(_ context.Context, task *Task) error { stored = task; return nil }}
	dispatcher, err := NewDispatcher("mail", store, obs)
	if err != nil {
		t.Fatal(err)
	}
	original := &Task{Type: "email", Payload: []byte("hello"), Headers: map[string]string{"custom": "value"}, AvailableAt: time.Now().Add(time.Hour)}
	id, err := dispatcher.Dispatch(context.Background(), original)
	if err != nil || id == "" || stored.ID != id || stored.Headers["traceparent"] == "" {
		t.Fatalf("dispatch %q %v %#v", id, err, stored)
	}
	stored.Payload[0] = 'X'
	stored.Headers["custom"] = "changed"
	if original.ID != "" || original.CreatedAt.IsZero() == false || string(original.Payload) != "hello" || original.Headers["custom"] != "value" || original.Headers["traceparent"] != "" {
		t.Fatal("mutated input")
	}
	if !stored.AvailableAt.Equal(original.AvailableAt) || len(exporter.GetSpans()) != 1 {
		t.Fatal("lost delay or trace")
	}
	failure := errors.New("backend failure")
	store.enqueue = func(context.Context, *Task) error { return failure }
	id, err = dispatcher.Dispatch(context.Background(), &Task{ID: "stable", Type: "email"})
	if id != "stable" || !errors.Is(err, failure) {
		t.Fatalf("identity/cause %q %v", id, err)
	}
	if _, err = dispatcher.Dispatch(context.Background(), nil); err == nil {
		t.Fatal("accepted nil")
	}
	if _, err = NewDispatcher(" ", store, obs); err == nil {
		t.Fatal("accepted empty name")
	}
}

func TestPrepareTaskValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		task *Task
	}{
		{"nil", nil}, {"type", &Task{}}, {"blank id", &Task{ID: " ", Type: "x"}}, {"long id", &Task{ID: strings.Repeat("x", 129), Type: "x"}}, {"header", &Task{Type: "x", Headers: map[string]string{" ": "v"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := prepareTask(tc.task, time.Now()); err == nil {
				t.Fatal("accepted invalid task")
			}
		})
	}
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	task, err := prepareTask(&Task{Type: " x "}, now)
	if err != nil || task.Type != "x" || !task.AvailableAt.Equal(now) || !task.CreatedAt.Equal(now) {
		t.Fatalf("defaults %#v %v", task, err)
	}
}
