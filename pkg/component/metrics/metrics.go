package metrics

import (
	"context"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/app_info"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

var (
	counterAlreadyExistsErr   = errors.New("counter already exists")
	gaugeAlreadyExistsErr     = errors.New("gauge already exists")
	histogramAlreadyExistsErr = errors.New("histogram already exists")
)

type Metrics struct {
	*log.Helper
	meterProvider metric.MeterProvider
	// 默认 meter
	meter metric.Meter

	// 应用的属性
	serviceAttrs []attribute.KeyValue

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

const logModule = "metrics"

func NewMetrics(
	conf *Config,
	appInfo *app_info.AppInfo,
	log *log.Log,
) (*Metrics, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, errors.WithMessage(err, "new prometheus failed")
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))

	serviceAttrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(appInfo.GetName()),
		semconv.ServiceInstanceIDKey.String(appInfo.GetId()),
		semconv.ServiceVersionKey.String(appInfo.GetVersion()),
	}

	meterName := utils.Select(conf.GetMeterName(), appInfo.GetName())
	meter := provider.Meter(meterName, metric.WithInstrumentationAttributes(serviceAttrs...))

	return &Metrics{
		Helper:        log.WithModule(logModule, conf.GetLog()).NewHelper(),
		meterProvider: provider,
		meter:         meter,
		serviceAttrs:  serviceAttrs,
		counterMap:    make(map[string]metric.Int64Counter, conf.GetCounterMapSize()),
		gaugeMap:      make(map[string]metric.Int64Gauge, conf.GetGaugeMapSize()),
		histogramMap:  make(map[string]metric.Float64Histogram, conf.GetHistogramMapSize()),
	}, nil
}

func (m *Metrics) ServiceAttrs() []attribute.KeyValue {
	return m.serviceAttrs
}

func (m *Metrics) GetMeterProvider() metric.MeterProvider {
	return m.meterProvider
}

func (m *Metrics) GetMeter() metric.Meter {
	return m.meter
}

// RegisterCounter 注册一个 counter
func (m *Metrics) RegisterCounter(name string, counter metric.Int64Counter) error {
	m.counterMux.Lock()
	defer m.counterMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.counterMap[name]; ok {
		return errors.WithMessage(counterAlreadyExistsErr, "name: "+name)
	}

	m.counterMap[name] = counter
	return nil
}

// RegisterNewCounter 注册一个新counter
func (m *Metrics) RegisterNewCounter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
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

// AddCounter 增加 counter
func (m *Metrics) AddCounter(ctx context.Context, name string, incr int64, options ...metric.AddOption) bool {
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
			m.WithContext(ctx).Warnf("AddCounter(%s, %d) failed: create new counter failed: %v", name, incr, err)
			return false
		}
		counter, _ = getCounter()
	}
	counter.Add(ctx, incr, options...)
	return true
}

// RegisterGauge 注册一个 gauge
func (m *Metrics) RegisterGauge(name string, gauge metric.Int64Gauge) error {
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

// RegisterNewGauge 注册一个新的 gauge
func (m *Metrics) RegisterNewGauge(name string, options ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
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

// RecordGauge 记录 gauge 的值
func (m *Metrics) RecordGauge(ctx context.Context, name string, value int64, options ...metric.RecordOption) bool {
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
			m.WithContext(ctx).Warnf("RecordGauge(%s, %d) failed: create new gauge failed: %v", name, value, err)
			return false
		}
		gauge, _ = getGauge()
	}

	gauge.Record(ctx, value, options...)
	return true
}

// RegisterHistogram 注册一个 histogram
func (m *Metrics) RegisterHistogram(name string, histogram metric.Float64Histogram) error {
	m.histogramMux.Lock()
	defer m.histogramMux.Unlock()

	// 如果已经存在，则不写入
	if _, ok := m.histogramMap[name]; ok {
		return errors.WithMessage(histogramAlreadyExistsErr, "name: "+name)
	}

	m.histogramMap[name] = histogram
	return nil
}

// RegisterNewHistogram 注册一个新的 histogram
func (m *Metrics) RegisterNewHistogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
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

// RecordHistogram 记录 histogram 的一个样本值
func (m *Metrics) RecordHistogram(ctx context.Context, name string, incr float64, options ...metric.RecordOption) bool {
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
			m.WithContext(ctx).Warnf("RecordHistogram(%s, %f) failed: create new histogram failed: %v", name, incr, err)
			return false
		}
		histogram, _ = getHistogram()
	}

	histogram.Record(ctx, incr, options...)
	return true
}

type metricsCtxKey struct{}

func NewContext(ctx context.Context, metric *Metrics) context.Context {
	return context.WithValue(ctx, metricsCtxKey{}, metric)
}

func FromContext(ctx context.Context) (metrics *Metrics, ok bool) {
	metrics, ok = ctx.Value(metricsCtxKey{}).(*Metrics)
	return
}

func AddCounter(ctx context.Context, name string, incr int64, options ...metric.AddOption) bool {
	metrics, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return metrics.AddCounter(ctx, name, incr, options...)
}

func RecordGauge(ctx context.Context, name string, value int64, options ...metric.RecordOption) bool {
	metrics, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return metrics.RecordGauge(ctx, name, value, options...)
}

func RecordHistogram(ctx context.Context, name string, incr float64, options ...metric.RecordOption) bool {
	metrics, ok := FromContext(ctx)
	if !ok {
		return ok
	}
	return metrics.RecordHistogram(ctx, name, incr, options...)
}
