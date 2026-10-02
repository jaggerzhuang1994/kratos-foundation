package database

import (
	"context"
	"database/sql"
	"errors"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/proto"
	gormmysql "gorm.io/driver/mysql"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	logger := &poolPolicyLogger{Logger: newManagerTestLogger(t)}
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
		Tracing: &config_pb.GormTracing{Disable: proto.Bool(false)},
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

	if record := logger.records[0]; record.level != kratoslog.LevelInfo || record.fields["event"] != "database.manager.ready" || record.fields["connections"] != 2 || !slices.Equal(record.fields["drivers"].([]string), []string{"sqlite3"}) {
		t.Fatalf("manager ready log=%+v", record)
	}
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
	if record := logger.records[len(logger.records)-1]; record.level != kratoslog.LevelInfo || record.fields["event"] != "database.manager.closed" {
		t.Fatalf("manager closed log=%+v", record)
	}
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
	shared, cleanup, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
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

func newSQLiteManager(t *testing.T, conf *config_pb.Database) *manager {
	t.Helper()
	conf.Metrics = &config_pb.GormMetrics{Disable: proto.Bool(true)}
	driver := new(recordingSQLiteDriver)
	m, close, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), newManagerMetricsProvider(), map[string]DriverFactory{"sqlite3": driver.open})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(close)
	return m
}
func TestManagerKeepsNamedDialectsIndependent(t *testing.T) {
	conf := &config_pb.Database{Default: proto.String("primary"), Gorm: &config_pb.Gorm{SkipDefaultTransaction: proto.Bool(true)}, Metrics: &config_pb.GormMetrics{Disable: proto.Bool(true)}, Connections: map[string]*config_pb.DBConnection{
		"primary": {Driver: proto.String("sqlite3"), Dsn: "file:review-dialect-primary?mode=memory&cache=shared"},
		"mysql":   {Driver: proto.String("mysql"), Dsn: "unused-local-dialect-test"},
	}}
	driver := new(recordingSQLiteDriver)
	mysqlFactory := func(config DriverConfig) (DriverConnection, error) {
		// SQLite pool substitutes only the network transport; GORM uses the real MySQL dialect.
		c, e := driver.open(DriverConfig{Name: config.Name, DSN: "file:review-dialect-mysql?mode=memory&cache=shared"})
		if e != nil {
			return c, e
		}
		c.Dialector = gormmysql.New(gormmysql.Config{Conn: c.SQLDB, SkipInitializeWithVersion: true})
		return c, nil
	}
	m, cleanup, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), newManagerMetricsProvider(), map[string]DriverFactory{"sqlite3": driver.open, "mysql": mysqlFactory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	q := m.Connection(UseConnection(context.Background(), "mysql")).Session(&gorm.Session{DryRun: true}).Clauses(clause.OnConflict{UpdateAll: true}).Create(&managerRecord{ID: 1, Name: "value"})
	if q.Error != nil {
		t.Fatal(q.Error)
	}
	t.Logf("target mysql, dialect=%s, SQL=%s", q.Dialector.Name(), q.Statement.SQL.String())
	if q.Dialector.Name() != "mysql" || !strings.Contains(q.Statement.SQL.String(), "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("MySQL connection built the wrong dialect: name=%s SQL=%s", q.Dialector.Name(), q.Statement.SQL.String())
	}
	if strings.Contains(q.Statement.SQL.String(), "RETURNING") {
		t.Error("MySQL upsert contains SQLite RETURNING clause")
	}
	p := m.Connection(UseConnection(context.Background(), "primary"))
	if err := p.AutoMigrate(&managerRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := p.Clauses(clause.OnConflict{UpdateAll: true}).Create(&managerRecord{ID: 1, Name: "value"}).Error; err != nil {
		t.Errorf("SQLite upsert broken by MySQL clause builder: %v", err)
	}
}

func TestNamedConnectionsKeepConfigurationAndAESBoundToTheirTransaction(t *testing.T) {
	const firstKey = "MDEyMzQ1Njc4OWFiY2RlZg=="
	const secondKey = "ZmVkY2JhOTg3NjU0MzIxMA=="
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Gorm:    &config_pb.Gorm{AllowGlobalUpdate: proto.Bool(true), SkipDefaultTransaction: proto.Bool(true)},
		Connections: map[string]*config_pb.DBConnection{
			"primary": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "primary.db"), Aes: &config_pb.DatabaseAes{Key: proto.String(firstKey)}},
			"archive": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "archive.db"), Aes: &config_pb.DatabaseAes{Key: proto.String(secondKey)}, Gorm: &config_pb.Gorm{AllowGlobalUpdate: proto.Bool(false), SkipDefaultTransaction: proto.Bool(false)}},
		},
	}
	manager := newSQLiteManager(t, config)
	ctx := context.Background()
	for _, name := range []string{"primary", "archive"} {
		db := manager.Connection(UseConnection(ctx, name))
		if err := db.AutoMigrate(&aesMapValuesRecord{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&aesMapValuesRecord{Secret: AESDecryptString(name), Plain: name}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Connection(ctx).Model(&aesMapValuesRecord{}).Update("Plain", "allowed").Error; err != nil {
		t.Fatalf("global configuration not inherited: %v", err)
	}
	if err := manager.Connection(UseConnection(ctx, "archive")).Model(&aesMapValuesRecord{}).Update("Plain", "blocked").Error; !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("explicit false override ignored: %v", err)
	}
	err := manager.Transaction(UseConnection(ctx, "archive"), func(txCtx context.Context) error {
		switched := UseConnection(txCtx, "primary")
		if err := manager.Connection(UseConnection(txCtx, "missing")).Error; !errors.Is(err, ErrConnectionUnknown) {
			t.Fatalf("unknown transaction selector=%v", err)
		}
		db := manager.Connection(switched)
		if db.SkipDefaultTransaction || db.AllowGlobalUpdate {
			t.Fatal("transaction inherited another connection's GORM configuration")
		}
		return db.Model(&aesMapValuesRecord{}).Where("plain = ?", "archive").Updates(map[string]any{"Secret": "transaction secret"}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, key, secret string }{{"primary", firstKey, "primary"}, {"archive", secondKey, "transaction secret"}} {
		pool, _ := manager.connectionFactory.pool(tt.name)
		var encrypted string
		if err := pool.db.QueryRow("SELECT secret FROM aes_map_values_records").Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		cipher, err := newAESFieldCipher(&config_pb.DatabaseAes{Key: proto.String(tt.key)})
		if err != nil {
			t.Fatal(err)
		}
		plain, err := cipher.algorithm.DecryptString(encrypted, string(cipher.key))
		if err != nil || plain != tt.secret {
			t.Fatalf("connection=%s decrypted=%q err=%v", tt.name, plain, err)
		}
		var record aesMapValuesRecord
		if err := manager.Connection(UseConnection(ctx, tt.name)).First(&record).Error; err != nil || string(record.Secret) != tt.secret {
			t.Fatalf("connection=%s record=%#v err=%v", tt.name, record, err)
		}
	}
}

// 公共入口选择并冻结全局登记快照；详细组装/SQL/事务行为由同文件核心用例验证。
func TestNewManagerOwnsRegisteredDriverSnapshot(t *testing.T) {
	for _, test := range []struct {
		name, driverName string
		wantErr          bool
	}{
		{"registered driver", "sqlite3", false},
		{"unknown driver", "missing", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := databaseDrivers
			databaseDrivers = newDriverRegistry()
			t.Cleanup(func() { databaseDrivers = previous })
			driver := new(recordingSQLiteDriver)
			MustRegisterDriver("sqlite3", driver.open)
			settings := &config_pb.Database{Default: proto.String("default"), Connections: map[string]*config_pb.DBConnection{
				"default": {Driver: proto.String(test.driverName), Dsn: ":memory:"},
			}, Metrics: &config_pb.GormMetrics{Disable: proto.Bool(true)}}
			manager, cleanup, err := NewManager(newManagerTestLogger(t), testconfig.New(t, "database", settings), managerTestAppInfo{}, newManagerTracingProvider(true), newManagerMetricsProvider())
			if (err != nil) != test.wantErr {
				t.Fatalf("NewManager error=%v wantErr=%v", err, test.wantErr)
			}
			// 快照由公开构造入口取得；成功或失败均不能在本进程晚注册 driver。
			if err := RegisterDriver("late", stubDriver); err == nil {
				t.Fatal("public constructor did not freeze registered driver snapshot")
			}
			calls, pools := driver.snapshots()
			if test.wantErr {
				if manager != nil || cleanup != nil || len(calls) != 0 {
					t.Fatalf("failed public constructor returned resource: manager=%v cleanup=%v calls=%v", manager, cleanup != nil, calls)
				}
				return
			}
			if manager == nil || cleanup == nil || len(calls) != 1 || calls[0].Name != "default" {
				t.Fatalf("public constructor used wrong factory: manager=%v cleanup=%v calls=%v", manager, cleanup != nil, calls)
			}
			t.Cleanup(cleanup)
			if manager.Connection(t.Context()).Dialector.Name() != "sqlite" {
				t.Fatal("registered factory dialect was not used")
			}
			cleanup()
			cleanup()
			if !errors.Is(manager.Connection(t.Context()).Error, ErrManagerClosed) {
				t.Fatal("public cleanup left Manager available")
			}
			if len(pools) != 1 || pools[0].Ping() == nil {
				t.Fatal("public cleanup left owned driver pool open")
			}
		})
	}
}
