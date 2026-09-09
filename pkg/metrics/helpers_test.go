package metrics

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/metric"
)

func TestInt64CounterRequiresMetricsContext(t *testing.T) {
	_, err := Int64Counter(context.Background(), "business_events")
	if !errors.Is(err, ErrMetricsNotFound) {
		t.Fatalf("Int64Counter error = %v, want %v", err, ErrMetricsNotFound)
	}
}

func TestInt64CounterUsesMetricsFromContext(t *testing.T) {
	appInfo := testAppInfo{name: "orders"}
	provider, cleanup, err := NewProvider(appInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := WithMetrics(context.Background(), NewMetrics(provider, appInfo))

	counter, err := Int64Counter(
		ctx,
		"context_business_events",
		metric.WithDescription("events recorded through context API"),
	)
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)

	labels := gatheredScopeLabels(t, provider)
	if got := labels["otel_scope_name"]; got != "orders" {
		t.Fatalf("context meter scope name = %q, want orders", got)
	}
}

func TestRemainingContextAPIsRequireMetrics(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Int64UpDownCounter",
			call: func() error {
				_, err := Int64UpDownCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Int64Histogram",
			call: func() error {
				_, err := Int64Histogram(ctx, "instrument")
				return err
			},
		},
		{
			name: "Int64Gauge",
			call: func() error {
				_, err := Int64Gauge(ctx, "instrument")
				return err
			},
		},
		{
			name: "Int64ObservableCounter",
			call: func() error {
				_, err := Int64ObservableCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Int64ObservableUpDownCounter",
			call: func() error {
				_, err := Int64ObservableUpDownCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Int64ObservableGauge",
			call: func() error {
				_, err := Int64ObservableGauge(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64Counter",
			call: func() error {
				_, err := Float64Counter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64UpDownCounter",
			call: func() error {
				_, err := Float64UpDownCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64Histogram",
			call: func() error {
				_, err := Float64Histogram(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64Gauge",
			call: func() error {
				_, err := Float64Gauge(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64ObservableCounter",
			call: func() error {
				_, err := Float64ObservableCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64ObservableUpDownCounter",
			call: func() error {
				_, err := Float64ObservableUpDownCounter(ctx, "instrument")
				return err
			},
		},
		{
			name: "Float64ObservableGauge",
			call: func() error {
				_, err := Float64ObservableGauge(ctx, "instrument")
				return err
			},
		},
		{
			name: "RegisterCallback",
			call: func() error {
				_, err := RegisterCallback(
					ctx,
					func(context.Context, metric.Observer) error { return nil },
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrMetricsNotFound) {
				t.Fatalf("error = %v, want %v", err, ErrMetricsNotFound)
			}
		})
	}
}
