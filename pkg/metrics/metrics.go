package metrics

import (
	"context"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/log"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

var WithUnit = metric.WithUnit
var WithDescription = metric.WithDescription
var WithExplicitBucketBoundaries = metric.WithExplicitBucketBoundaries

type Metrics interface {
	GetMeterProvider() metric.MeterProvider
	GetMeter() metric.Meter
	GetMeterName() string

	RegisterCounter(name string, counter metric.Int64Counter) error
	RegisterNewCounter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error)
	AddCounter(ctx context.Context, name string, incr int64, options ...metric.AddOption) bool

	RegisterGauge(name string, gauge metric.Int64Gauge) error
	RegisterNewGauge(name string, options ...metric.Int64GaugeOption) (metric.Int64Gauge, error)
	RecordGauge(ctx context.Context, name string, value int64, options ...metric.RecordOption) bool

	RegisterHistogram(name string, histogram metric.Float64Histogram) error
	RegisterNewHistogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error)
	RecordHistogram(ctx context.Context, name string, incr float64, options ...metric.RecordOption) bool
}

type metrics struct {
	log    log.Log
	config Config

	mp    metric.MeterProvider
	meter metric.Meter

	// 累积量
	counterMux sync.RWMutex
	counterMap map[string]metric.Int64Counter

	// 瞬时量
	gaugeMux sync.RWMutex
	gaugeMap map[string]metric.Int64Gauge

	// 耗时
	histogramMux sync.RWMutex
	histogramMap map[string]metric.Float64Histogram
}

func NewMetrics(
	log log.Log,
	config Config,
	serviceAttrs app_info.ServiceAttributes,
) (Metrics, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, errors.WithMessage(err, "new exporters/prometheus failed")
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(resource.NewSchemaless(
			serviceAttrs...,
		)),
	)

	meter := mp.Meter(config.GetMeterName(), metric.WithInstrumentationAttributes(serviceAttrs...))

	return &metrics{
		log:    log.WithModule("metrics", config.GetLog()),
		config: config,

		mp:           mp,
		meter:        meter,
		counterMap:   make(map[string]metric.Int64Counter, config.GetCounterMapSize()),
		gaugeMap:     make(map[string]metric.Int64Gauge, config.GetGaugeMapSize()),
		histogramMap: make(map[string]metric.Float64Histogram, config.GetHistogramMapSize()),
	}, nil
}

func (m *metrics) GetMeterProvider() metric.MeterProvider {
	return m.mp
}

func (m *metrics) GetMeter() metric.Meter {
	return m.meter
}

func (m *metrics) GetMeterName() string {
	return m.config.GetMeterName()
}

func (m *metrics) RegisterCounter(name string, counter metric.Int64Counter) error {
	m.counterMux.Lock()
	defer m.counterMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.counterMap[name]; ok {
		return errors.WithMessage(counterAlreadyExistsErr, "name: "+name)
	}

	m.counterMap[name] = counter
	return nil
}

func (m *metrics) RegisterNewCounter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	m.counterMux.Lock()
	defer m.counterMux.Unlock()

	counter, err := m.meter.Int64Counter(name, options...)
	if err != nil {
		return nil, err
	}

	// 如果已经存在，则不写入
	if _, ok := m.counterMap[name]; ok {
		return nil, errors.WithMessage(counterAlreadyExistsErr, "name: "+name)
	}

	m.counterMap[name] = counter
	return counter, nil
}

func (m *metrics) AddCounter(ctx context.Context, name string, incr int64, options ...metric.AddOption) bool {
	getCounter := func() (metric.Int64Counter, bool) {
		m.counterMux.RLock()
		defer m.counterMux.RUnlock()
		counter, ok := m.counterMap[name]
		return counter, ok
	}
	counter, ok := getCounter()

	// counter 不存在，则初始化一个新的
	var err error
	if !ok {
		_, err = m.RegisterNewCounter(name)
		// 如果是已存在，则在读一次
		if !errors.Is(err, counterAlreadyExistsErr) && err != nil {
			m.log.WithContext(ctx).Warnf("AddCounter(%s, %d) failed: create new counter failed: %v", name, incr, err)
			return false
		}
		counter, _ = getCounter()
	}
	counter.Add(ctx, incr, options...)
	return true
}

func (m *metrics) RegisterGauge(name string, gauge metric.Int64Gauge) error {
	m.gaugeMux.Lock()
	defer m.gaugeMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.gaugeMap[name]; ok {
		return errors.WithMessage(gaugeAlreadyExistsErr, "name: "+name)
	}

	// 懒加载 map（如果你别处已经初始化，就可以删掉这段）
	if m.gaugeMap == nil {
		m.gaugeMap = make(map[string]metric.Int64Gauge)
	}

	m.gaugeMap[name] = gauge
	return nil
}

func (m *metrics) RegisterNewGauge(name string, options ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	m.gaugeMux.Lock()
	defer m.gaugeMux.Unlock()

	// 懒加载 map
	if m.gaugeMap == nil {
		m.gaugeMap = make(map[string]metric.Int64Gauge)
	}

	// 如果已经存在，则不写入
	if _, ok := m.gaugeMap[name]; ok {
		return nil, errors.WithMessage(gaugeAlreadyExistsErr, "name: "+name)
	}

	gauge, err := m.meter.Int64Gauge(name, options...)
	if err != nil {
		return nil, err
	}

	m.gaugeMap[name] = gauge
	return gauge, nil
}

func (m *metrics) RecordGauge(ctx context.Context, name string, value int64, options ...metric.RecordOption) bool {
	getGauge := func() (metric.Int64Gauge, bool) {
		m.gaugeMux.RLock()
		defer m.gaugeMux.RUnlock()
		gauge, ok := m.gaugeMap[name]
		return gauge, ok
	}

	gauge, ok := getGauge()

	// gauge 不存在，则初始化一个新的
	var err error
	if !ok {
		_, err = m.RegisterNewGauge(name)
		// 如果是已存在，则再读一次
		if !errors.Is(err, gaugeAlreadyExistsErr) && err != nil {
			m.log.WithContext(ctx).Warnf("RecordGauge(%s, %d) failed: create new gauge failed: %v", name, value, err)
			return false
		}
		gauge, _ = getGauge()
	}

	gauge.Record(ctx, value, options...)
	return true
}

func (m *metrics) RegisterHistogram(name string, histogram metric.Float64Histogram) error {
	m.histogramMux.Lock()
	defer m.histogramMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.histogramMap[name]; ok {
		return errors.WithMessage(histogramAlreadyExistsErr, "name: "+name)
	}

	m.histogramMap[name] = histogram
	return nil
}

func (m *metrics) RegisterNewHistogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	m.histogramMux.Lock()
	defer m.histogramMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.histogramMap[name]; ok {
		return nil, errors.WithMessage(histogramAlreadyExistsErr, "name: "+name)
	}

	histogram, err := m.meter.Float64Histogram(name, options...)
	if err != nil {
		return nil, err
	}

	m.histogramMap[name] = histogram
	return histogram, nil
}

func (m *metrics) RecordHistogram(ctx context.Context, name string, incr float64, options ...metric.RecordOption) bool {
	getHistogram := func() (metric.Float64Histogram, bool) {
		m.histogramMux.RLock()
		defer m.histogramMux.RUnlock()
		histogram, ok := m.histogramMap[name]
		return histogram, ok
	}

	histogram, ok := getHistogram()

	// histogram 不存在，则初始化一个新的
	var err error
	if !ok {
		_, err = m.RegisterNewHistogram(name)
		// 如果是已存在，则再读一次
		if !errors.Is(err, histogramAlreadyExistsErr) && err != nil {
			m.log.WithContext(ctx).Warnf("RecordHistogram(%s, %f) failed: create new histogram failed: %v", name, incr, err)
			return false
		}
		histogram, _ = getHistogram()
	}

	histogram.Record(ctx, incr, options...)
	return true
}
