package lock_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/metric"
)

type observedTestLease struct {
	err      error
	released bool
}

func (*observedTestLease) Key() string                                    { return "private-order-id" }
func (l *observedTestLease) TTL(context.Context) (time.Duration, error)   { return time.Second, l.err }
func (l *observedTestLease) Refresh(context.Context, time.Duration) error { return l.err }
func (l *observedTestLease) Unlock(context.Context) error {
	if l.err != nil {
		return l.err
	}
	if l.released {
		return lock.ErrNotHeld
	}
	l.released = true
	return nil
}

type observedTestLocker struct {
	lease lock.Lease
	err   error
}

func (l observedTestLocker) Lock(context.Context, string, time.Duration) (lock.Lease, error) {
	return l.lease, l.err
}
func (l observedTestLocker) TryLock(context.Context, string, time.Duration) (lock.Lease, error) {
	return l.lease, l.err
}

func TestWithMetrics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		result string
	}{
		{"success", nil, "success"}, {"contended", fmt.Errorf("wrapped: %w", lock.ErrNotAcquired), "contended"},
		{"lost", lock.ErrNotHeld, "not_held"}, {"timeout", context.DeadlineExceeded, "timeout"},
		{"cancel", context.Canceled, "canceled"}, {"error", errors.New("private failure"), "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider, cleanup, err := metrics.NewProvider(appinfo.New("lock-test"))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			underlying := &observedTestLease{}
			wrapped, err := lock.WithMetrics(observedTestLocker{underlying, tc.err}, "orders", provider)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for _, acquire := range []func(context.Context, string, time.Duration) (lock.Lease, error){wrapped.Lock, wrapped.TryLock} {
				lease, err := acquire(ctx, "private-order-id", time.Second)
				if !errors.Is(err, tc.err) {
					t.Fatalf("error changed: %v", err)
				}
				if err == nil {
					if lease.Key() != underlying.Key() {
						t.Fatal("key changed")
					}
					ttl, err := lease.TTL(ctx)
					if err != nil || ttl != time.Second {
						t.Fatalf("TTL: %v %v", ttl, err)
					}
					if err := lease.Refresh(ctx, time.Second); err != nil {
						t.Fatal(err)
					}
					underlying.err = lock.ErrNotHeld
					if _, err := lease.TTL(ctx); !errors.Is(err, lock.ErrNotHeld) {
						t.Fatal(err)
					}
					if err := lease.Refresh(ctx, time.Second); !errors.Is(err, lock.ErrNotHeld) {
						t.Fatal(err)
					}
					if err := lease.Unlock(ctx); !errors.Is(err, lock.ErrNotHeld) {
						t.Fatal(err)
					}
					underlying.err = nil
					underlying.released = false
					if err := lease.Unlock(ctx); err != nil {
						t.Fatal(err)
					}
					if err := lease.Unlock(ctx); !errors.Is(err, lock.ErrNotHeld) {
						t.Fatal(err)
					}
				}
			}
			families, err := provider.PrometheusGatherer().Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"lock", "try_lock"} {
				sample := findObservedSample(t, families, "lock_operations_total", map[string]string{"operation": operation, "result": tc.result})
				if sample.GetCounter().GetValue() != 1 {
					t.Fatal("acquisition count")
				}
				duration := findObservedSample(t, families, "lock_operation_duration_seconds", map[string]string{"operation": operation, "result": tc.result})
				if duration.GetHistogram().GetSampleCount() != 1 {
					t.Fatal("duration count")
				}
			}
			for _, family := range families {
				for _, sample := range family.Metric {
					for _, label := range sample.Label {
						if label.GetValue() == "private-order-id" || label.GetValue() == "private failure" {
							t.Fatal("sensitive/high cardinality label")
						}
					}
				}
			}
			if tc.err == nil {
				hold := findObservedSample(t, families, "lock_released_hold_duration_seconds", map[string]string{})
				if hold.GetHistogram().GetSampleCount() != 2 {
					t.Fatal("failed/duplicate unlock recorded holding time")
				}
			} else {
				for _, f := range families {
					if f.GetName() == "lock_released_hold_duration_seconds" {
						t.Fatal("failed acquisition recorded hold")
					}
				}
			}
		})
	}
}

func findObservedSample(t *testing.T, families []*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.Metric {
			got := map[string]string{}
			for _, label := range sample.Label {
				got[label.GetName()] = label.GetValue()
			}
			matched := got["lock_name"] == "orders"
			for k, v := range labels {
				matched = matched && got[k] == v
			}
			if matched {
				return sample
			}
		}
	}
	t.Fatalf("missing %s %v", name, labels)
	return nil
}

type failingMetricsProvider struct {
	metrics.Provider
	instrument string
}

func (p failingMetricsProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return failingLockMeter{Meter: p.Provider.Meter("test"), instrument: p.instrument}
}

type failingLockMeter struct {
	metric.Meter
	instrument string
}

func (m failingLockMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == m.instrument {
		return nil, errors.New("instrument failed")
	}
	return m.Meter.Int64Counter(name, opts...)
}
func (m failingLockMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if name == m.instrument {
		return nil, errors.New("instrument failed")
	}
	return m.Meter.Float64Histogram(name, opts...)
}
func TestWithMetricsRejectsInvalidConfiguration(t *testing.T) {
	for _, name := range []string{"", " orders", "orders "} {
		if _, err := lock.WithMetrics(nil, name, nil); err == nil {
			t.Fatal("accepted invalid name")
		}
	}
	provider, cleanup, err := metrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, name := range []string{"lock_operations_total", "lock_operation_duration_seconds", "lock_released_hold_duration_seconds"} {
		if _, err := lock.WithMetrics(observedTestLocker{}, "orders", failingMetricsProvider{provider, name}); err == nil {
			t.Fatal("instrument error ignored")
		}
	}
}
