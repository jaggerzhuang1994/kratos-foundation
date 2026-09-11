package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"go.opentelemetry.io/otel/metric"
)

func TestCacheMetrics(t *testing.T) {
	p, cleanup, err := NewProvider(appinfo.New("cache-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	c, err := NewCacheMetrics(p, "products")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c.Hit(ctx)
	c.Hit(ctx)
	c.Miss(ctx)
	c.Error(ctx)
	c.Load(ctx, time.Second, nil)
	c.Load(ctx, 2*time.Second, errors.New("private dependency error"))
	fs, err := p.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range fs {
		for _, m := range f.Metric {
			result := ""
			name := ""
			for _, l := range m.Label {
				if l.GetName() == "result" {
					result = l.GetValue()
				}
				if l.GetName() == "cache_name" {
					name = l.GetValue()
				}
			}
			if name != "products" {
				continue
			}
			key := f.GetName() + "/" + result
			if m.Counter != nil {
				got[key] = m.Counter.GetValue()
			}
			if m.Histogram != nil {
				got[key] = m.Histogram.GetSampleSum()
				got[key+"/count"] = float64(m.Histogram.GetSampleCount())
			}
		}
	}
	want := map[string]float64{"business_cache_lookups_total/hit": 2, "business_cache_lookups_total/miss": 1, "business_cache_lookups_total/error": 1, "business_cache_loads_total/success": 1, "business_cache_loads_total/error": 1, "business_cache_load_duration_seconds/success": 1, "business_cache_load_duration_seconds/error": 2, "business_cache_load_duration_seconds/success/count": 1, "business_cache_load_duration_seconds/error/count": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%v want %v", k, got[k], v)
		}
	}
}

type cacheFailProvider struct {
	Provider
	name string
}

func (p cacheFailProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return cacheFailMeter{Meter: p.Provider.Meter("cache-test"), name: p.name}
}

type cacheFailMeter struct {
	metric.Meter
	name string
}

func (m cacheFailMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == m.name {
		return nil, errors.New("failed")
	}
	return m.Meter.Int64Counter(name, opts...)
}
func (m cacheFailMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if name == m.name {
		return nil, errors.New("failed")
	}
	return m.Meter.Float64Histogram(name, opts...)
}
func TestCacheMetricsConstructionErrors(t *testing.T) {
	for _, name := range []string{"", " bad", "bad "} {
		if _, err := NewCacheMetrics(nil, name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	p, cleanup, err := NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, name := range []string{"business_cache_lookups_total", "business_cache_loads_total", "business_cache_load_duration_seconds"} {
		if _, err := NewCacheMetrics(cacheFailProvider{p, name}, "products"); err == nil {
			t.Fatal("instrument error ignored")
		}
	}
}
