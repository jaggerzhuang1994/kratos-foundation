package database

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"gorm.io/gorm"
)

// ErrManagerClosed 表示 Manager 已关闭。
var ErrManagerClosed = errors.New("database manager is closed")

// ErrConnectionUnknown 表示 Context 选择了当前 Manager 未配置的具名连接。
var ErrConnectionUnknown = errors.New("database connection is not configured")

// Manager 提供请求级数据库访问；连接池由 cleanup 统一释放。
type Manager interface {
	Connection(context.Context) *gorm.DB
	TransactionManager
}

// manager 保存各连接独立的 GORM 根实例和关闭状态。
type manager struct {
	db                *gorm.DB
	log               log.Logger
	connections       map[string]*gorm.DB
	connectionFactory *connectionFactory
	metricsCollector  *metricsCollector

	stateMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// newManagerWithDrivers 使用固定驱动快照组装连接、插件与指标。
func newManagerWithDrivers(
	log log.Logger,
	configManager foundationconfig.Manager,
	appInfo appinfo.AppInfo,
	tracingProvider tracing.Provider,
	metricsProvider foundationmetrics.Provider,
	drivers map[string]DriverFactory,
) (*manager, func(), error) {
	config, err := loadConfig(configManager, drivers)
	if err != nil {
		return nil, nil, err
	}
	databaseLogger, err := log.WithModuleConfig("database", config.GetLog())
	if err != nil {
		return nil, nil, fmt.Errorf("configure database logger: %w", err)
	}
	gormModuleLogger, err := log.WithModuleConfig("database/gorm", config.GetLog())
	if err != nil {
		return nil, nil, fmt.Errorf("configure database GORM logger: %w", err)
	}
	connectionFactory := newConnectionFactory(drivers)
	fail := func(err error) (*manager, func(), error) {
		return nil, nil, closeConnectionFactoryAfterError(connectionFactory, err)
	}
	// 每个具名连接单独 gorm.Open，方言、ClauseBuilders、回调和配置均不共享。
	connections := make(map[string]*gorm.DB, len(config.GetConnections()))
	for _, name := range slices.Sorted(maps.Keys(config.GetConnections())) {
		connection := config.GetConnections()[name]
		aesPlugin, err := newAESFieldPlugin(name, connection.GetAes())
		if err != nil {
			return fail(err)
		}
		dialector, err := connectionFactory.make(name, connection)
		if err != nil {
			return fail(err)
		}
		effective := mergeGORMConfig(config.GetGorm(), connection.GetGorm())
		db, err := gorm.Open(dialector, newGORMConfig(effective, newGORMLogger(gormModuleLogger, effective.GetLogger())))
		if err != nil {
			return fail(fmt.Errorf("open database connection %q: %w", name, err))
		}
		if tracingPlugin := newTracingPlugin(config, appInfo, tracingProvider); tracingPlugin != nil {
			if err := db.Use(tracingPlugin); err != nil {
				return fail(fmt.Errorf("register database tracing plugin for %q: %w", name, err))
			}
		}
		if err := db.Use(aesPlugin); err != nil {
			return fail(fmt.Errorf("register database AES field plugin for %q: %w", name, err))
		}
		connections[name] = db
	}
	metricsCollector, err := newMetricsCollector(
		connectionFactory,
		config,
		appInfo,
		databaseLogger,
		metricsProvider,
	)
	if err != nil {
		return fail(err)
	}

	if metricsCollector != nil {
		metricsCollector.sql, err = newSQLMetrics(metricsCollector.registerer)
		if err != nil {
			metricsCollector.close()
			return fail(err)
		}
		for name, db := range connections {
			effective := mergeGORMConfig(config.GetGorm(), config.GetConnections()[name].GetGorm())
			if err := metricsCollector.sql.install(db, name, effective.GetLogger().GetSlowThreshold().AsDuration()); err != nil {
				metricsCollector.close()
				return fail(fmt.Errorf("install database SQL metrics for %q: %w", name, err))
			}
		}
	}

	// 连接池参数在连接建立后才能写回，因此订阅放在全部连接就绪之后。
	cancelPoolUpdates, err := subscribeConnectionPools(
		configManager,
		databaseLogger,
		config,
		connectionFactory,
		drivers,
	)
	if err != nil {
		metricsCollector.close()
		return fail(err)
	}

	result := &manager{
		db:                connections[config.GetDefault()],
		log:               databaseLogger,
		connections:       connections,
		connectionFactory: connectionFactory,
		metricsCollector:  metricsCollector,
	}
	var cleanupOnce sync.Once
	return result, func() {
		cleanupOnce.Do(func() {
			// 先停订阅再关连接，避免回调向已关闭的连接池写入参数。
			cancelPoolUpdates()
			if closeErr := result.close(); closeErr != nil {
				result.log.With("error", closeErr).Error("manager.close | database cleanup failed")
			}
		})
	}, nil
}

// close 幂等停止指标采集并释放 Manager 拥有的全部连接池。
func (mgr *manager) close() error {
	mgr.closeOnce.Do(func() {
		mgr.stateMu.Lock()
		mgr.closed = true
		factory := mgr.connectionFactory
		collector := mgr.metricsCollector
		mgr.metricsCollector = nil
		mgr.stateMu.Unlock()
		// 先停采集再关连接，避免后台 SHOW STATUS 与连接释放相互竞争。
		collector.close()
		mgr.closeErr = factory.close()
	})
	return mgr.closeErr
}

// Connection 返回绑定请求 Context、事务与显式连接选择的 GORM 会话。
func (mgr *manager) Connection(ctx context.Context) *gorm.DB {
	mgr.stateMu.RLock()
	closed := mgr.closed
	mgr.stateMu.RUnlock()
	if closed {
		db := mgr.db.Session(&gorm.Session{})
		db.Error = ErrManagerClosed
		return db
	}
	selected := mgr.db
	if name, explicit := getConnection(ctx); explicit {
		var ok bool
		selected, ok = mgr.connections[name]
		if !ok {
			db := mgr.db.WithContext(ctx)
			db.Error = fmt.Errorf("%w: %q", ErrConnectionUnknown, name)
			return db
		}
	}
	if tx, ok := getTx(ctx, mgr); ok {
		// 事务固定已选连接；派生 Context 只更新超时、链路和值，不改变事务所属数据库。
		return tx.WithContext(ctx)
	}
	return selected.WithContext(ctx)
}

// NewManager 在一个入口内完成连接、GORM 插件与指标采集组装。
func NewManager(
	logger log.Logger,
	configManager foundationconfig.Manager,
	appInfo appinfo.AppInfo,
	tracingProvider tracing.Provider,
	metricsProvider foundationmetrics.Provider,
) (Manager, func(), error) {
	manager, cleanup, err := newManagerWithDrivers(
		logger,
		configManager,
		appInfo,
		tracingProvider,
		metricsProvider,
		driverSnapshot(),
	)
	if err != nil {
		return nil, nil, err
	}
	return manager, cleanup, nil
}
