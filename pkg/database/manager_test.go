package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type managerTestAppInfo struct{}

func (managerTestAppInfo) ID() string      { return "database-test-id" }
func (managerTestAppInfo) Name() string    { return "database-test" }
func (managerTestAppInfo) Version() string { return "v1.0.0" }
func (managerTestAppInfo) Metadata() map[string]string {
	return map[string]string{"env": "test", "hostname": "localhost"}
}

type managerTracingProvider struct {
	disabled bool
	provider trace.TracerProvider
}

func newManagerTracingProvider(disabled bool) managerTracingProvider {
	return managerTracingProvider{disabled: disabled, provider: tracenoop.NewTracerProvider()}
}

func (p managerTracingProvider) Disabled() bool { return p.disabled }
func (p managerTracingProvider) TracerProvider() trace.TracerProvider {
	return p.provider
}
func (p managerTracingProvider) Tracer(
	name string,
	options ...trace.TracerOption,
) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

type managerMetricsProvider struct {
	registry *prometheus.Registry
	provider metric.MeterProvider
}

func newManagerMetricsProvider() *managerMetricsProvider {
	return &managerMetricsProvider{
		registry: prometheus.NewRegistry(),
		provider: metricnoop.NewMeterProvider(),
	}
}

func (p *managerMetricsProvider) Meter(
	name string,
	options ...metric.MeterOption,
) metric.Meter {
	return p.provider.Meter(name, options...)
}
func (p *managerMetricsProvider) MeterProvider() metric.MeterProvider {
	return p.provider
}
func (p *managerMetricsProvider) PrometheusGatherer() prometheus.Gatherer {
	return p.registry
}
func (p *managerMetricsProvider) PrometheusRegisterer() prometheus.Registerer {
	return p.registry
}

type recordingSQLiteDriver struct {
	mu    sync.Mutex
	calls []DriverConfig
	dbs   []*sql.DB
}

func (driver *recordingSQLiteDriver) open(config DriverConfig) (DriverConnection, error) {
	db, err := sql.Open(gormsqlite.DriverName, config.DSN)
	if err != nil {
		return DriverConnection{}, err
	}
	driver.mu.Lock()
	driver.calls = append(driver.calls, config)
	driver.dbs = append(driver.dbs, db)
	driver.mu.Unlock()
	return DriverConnection{
		SQLDB: db,
		Dialector: gormsqlite.New(gormsqlite.Config{
			DSN:  config.DSN,
			Conn: db,
		}),
	}, nil
}

func (driver *recordingSQLiteDriver) snapshots() ([]DriverConfig, []*sql.DB) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]DriverConfig(nil), driver.calls...), append([]*sql.DB(nil), driver.dbs...)
}

type trackingDatabaseConfig struct {
	foundationconfig.Manager
	subscribeErr error
	cancelCount  atomic.Int32
}

func (manager *trackingDatabaseConfig) Subscribe(
	key string,
	prototype any,
	observer foundationconfig.Observer,
	defaultValue ...any,
) (func(), error) {
	if manager.subscribeErr != nil {
		return nil, manager.subscribeErr
	}
	cancel, err := manager.Manager.Subscribe(key, prototype, observer, defaultValue...)
	if err != nil {
		return nil, err
	}
	return func() {
		cancel()
		manager.cancelCount.Add(1)
	}, nil
}

type managerRecord struct {
	ID   int `gorm:"primaryKey"`
	Name string
}

func (managerRecord) TableName() string { return "manager_records" }

func TestNewManagerAssemblesSQLiteConnectionsPluginsAndCleanup(t *testing.T) {
	logger := newManagerTestLogger(t)
	appInfo := managerTestAppInfo{}
	tracingProvider := newManagerTracingProvider(false)
	metricsProvider := newManagerMetricsProvider()
	metricsDisabled := true
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Connections: map[string]*config_pb.DBConnection{
			"primary": {
				Driver: proto.String("sqlite3"),
				Dsn:    "file:manager-primary?mode=memory&cache=shared",
			},
			"analytics": {
				Driver: proto.String(" SQLITE3 "),
				Dsn:    "file:manager-analytics?mode=memory&cache=shared",
			},
		},
		Metrics: &config_pb.GormMetrics{Disable: &metricsDisabled},
	}
	configManager := &trackingDatabaseConfig{
		Manager: testconfig.New(t, "database", config),
	}
	driver := new(recordingSQLiteDriver)

	manager, cleanup, err := newManagerWithDrivers(
		logger,
		configManager,
		appInfo,
		tracingProvider,
		metricsProvider,
		map[string]DriverFactory{"sqlite3": driver.open},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || cleanup == nil {
		t.Fatalf(
			"newManagerWithDrivers(): manager nil = %t, cleanup nil = %t",
			manager == nil,
			cleanup == nil,
		)
	}
	t.Cleanup(cleanup)

	calls, pools := driver.snapshots()
	if len(calls) != 2 || len(pools) != 2 {
		t.Fatalf("driver calls = %#v, pools = %d", calls, len(pools))
	}
	calledDSNs := make(map[string]string, len(calls))
	for _, call := range calls {
		calledDSNs[call.Name] = call.DSN
	}
	if calledDSNs["primary"] != config.GetConnections()["primary"].GetDsn() ||
		calledDSNs["analytics"] != config.GetConnections()["analytics"].GetDsn() {
		t.Fatalf("driver calls = %#v", calls)
	}

	for _, pluginName := range []string{
		new(aesFieldPlugin).Name(),
		newTracingPlugin(config, appInfo, tracingProvider).Name(),
	} {
		if _, ok := manager.db.Config.Plugins[pluginName]; !ok {
			t.Errorf("GORM plugin %q was not registered; plugins = %v", pluginName, manager.db.Config.Plugins)
		}
	}

	ctx := context.Background()
	primary := manager.Connection(UseConnection(ctx, "primary"))
	analytics := manager.Connection(UseConnection(ctx, "analytics"))
	if err := primary.AutoMigrate(&managerRecord{}); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	if err := analytics.AutoMigrate(&managerRecord{}); err != nil {
		t.Fatalf("migrate analytics: %v", err)
	}
	if err := primary.Create(&managerRecord{ID: 1, Name: "primary"}).Error; err != nil {
		t.Fatalf("insert primary: %v", err)
	}
	if err := analytics.Create(&managerRecord{ID: 2, Name: "analytics"}).Error; err != nil {
		t.Fatalf("insert analytics: %v", err)
	}
	assertManagerRecord(t, primary, 1, "primary")
	assertManagerRecord(t, analytics, 2, "analytics")
	assertManagerRecord(t, manager.Connection(ctx), 1, "primary")
	if err := manager.Connection(UseConnection(ctx, "missing")).Error; !errors.Is(err, ErrConnectionUnknown) {
		t.Fatalf("unknown connection error = %v", err)
	}

	cleanup()
	cleanup()
	if got := configManager.cancelCount.Load(); got != 1 {
		t.Fatalf("subscription cancel count = %d, want 1", got)
	}
	if err := manager.Connection(ctx).Error; !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection() after cleanup error = %v", err)
	}
	for index, db := range pools {
		if err := db.Ping(); err == nil {
			t.Errorf("pool %d remains open after cleanup", index)
		}
	}
}

func TestNewManagerRollsBackConnectionsAndMetricsWhenSubscriptionFails(t *testing.T) {
	logger := newManagerTestLogger(t)
	appInfo := managerTestAppInfo{}
	tracingProvider := newManagerTracingProvider(true)
	metricsProvider := newManagerMetricsProvider()
	tracingDisabled := true
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Connections: map[string]*config_pb.DBConnection{
			"primary": {
				Driver: proto.String("sqlite3"),
				Dsn:    "file:manager-rollback?mode=memory&cache=shared",
			},
		},
		Tracing: &config_pb.GormTracing{Disable: &tracingDisabled},
	}
	wantErr := errors.New("subscribe database config")
	failingConfig := &trackingDatabaseConfig{
		Manager:      testconfig.New(t, "database", config),
		subscribeErr: wantErr,
	}
	firstDriver := new(recordingSQLiteDriver)

	manager, cleanup, err := newManagerWithDrivers(
		logger,
		failingConfig,
		appInfo,
		tracingProvider,
		metricsProvider,
		map[string]DriverFactory{"sqlite3": firstDriver.open},
	)
	if !errors.Is(err, wantErr) || manager != nil || cleanup != nil {
		t.Fatalf(
			"newManagerWithDrivers(): manager nil = %t, cleanup nil = %t, error = %v",
			manager == nil,
			cleanup == nil,
			err,
		)
	}
	_, failedPools := firstDriver.snapshots()
	if len(failedPools) != 1 {
		t.Fatalf("opened pools = %d, want 1", len(failedPools))
	}
	if err := failedPools[0].Ping(); err == nil {
		t.Fatal("failed construction left its SQLite pool open")
	}

	// Reusing the same metrics registry proves that failure also unregistered
	// collectors installed before Subscribe returned its error.
	retryDriver := new(recordingSQLiteDriver)
	retryManager, retryCleanup, err := newManagerWithDrivers(
		logger,
		testconfig.New(t, "database", config),
		appInfo,
		tracingProvider,
		metricsProvider,
		map[string]DriverFactory{"sqlite3": retryDriver.open},
	)
	if err != nil {
		t.Fatalf("retry after failed construction: %v", err)
	}
	if retryManager == nil || retryCleanup == nil {
		t.Fatalf(
			"retry newManagerWithDrivers(): manager nil = %t, cleanup nil = %t",
			retryManager == nil,
			retryCleanup == nil,
		)
	}
	t.Cleanup(retryCleanup)
	retryCleanup()
	_, retryPools := retryDriver.snapshots()
	if len(retryPools) != 1 || retryPools[0].Ping() == nil {
		t.Fatalf("retry cleanup did not close its pool: pools=%d", len(retryPools))
	}
}

func newManagerTestLogger(t testing.TB) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: foundationlog.FileConfig{OutputConfig: foundationlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared)
}

func assertManagerRecord(t testing.TB, db *gorm.DB, id int, wantName string) {
	t.Helper()
	var record managerRecord
	if err := db.First(&record, id).Error; err != nil {
		t.Fatalf("load record %d: %v", id, err)
	}
	if record.Name != wantName {
		t.Fatalf("record %d name = %q, want %q", id, record.Name, wantName)
	}
}
