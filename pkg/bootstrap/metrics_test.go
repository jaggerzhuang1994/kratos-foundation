package bootstrap_test

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestBootstrapAllowsRepeatedMetricsContextContributions(t *testing.T) {
	spec := app.NewSpec()
	meter := noop.NewMeterProvider().Meter("test")
	got, err := bootstrap.NewMetricsBootstrap(spec, meter)
	if err != nil {
		t.Fatal(err)
	}
	if got != (bootstrap.MetricsBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
	got, err = bootstrap.NewMetricsBootstrap(spec, meter)
	if err != nil {
		t.Fatalf("repeated bootstrap error = %v", err)
	}
	if got != (bootstrap.MetricsBootstrap{}) {
		t.Fatalf("bootstrap = %#v, want zero value", got)
	}
}
