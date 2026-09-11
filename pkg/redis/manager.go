package redis

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
)

// ErrManagerClosed 表示 Manager 已关闭，不能再解析连接。
var ErrManagerClosed = errors.New("redis manager is closed")

// Manager 持有 Default 和 Connection 返回的全部 Redis client。
type Manager interface {
	// Default 返回配置的默认 client；Manager 关闭后返回 nil。
	Default() *redis.Client
	// Connection 返回或延迟创建具名 client。
	Connection(name string) (*redis.Client, error)
}

// todo 想让 Manager 默认实现 redis Client 的一些方法

// manager 持有全部 Redis client、配置快照和关闭状态。
type manager struct {
	log.Logger
	tracing tracing.Provider
	metrics metrics.Provider

	conf        componentConfig
	connOptions map[string]connectionOption

	mu          sync.Mutex
	connections map[string]*redis.Client
	defaultConn *redis.Client
	closed      bool
	closeOnce   sync.Once
	closeErr    error
}

// NewManager 初始化默认连接，并返回可关闭全部连接的幂等 cleanup。
func NewManager(
	log log.Logger,
	configManager foundationconfig.Manager,
	tracingProvider tracing.Provider,
	metricsProvider metrics.Provider,
) (Manager, func(), error) {
	config, err := loadConfig(configManager)
	if err != nil {
		return nil, nil, err
	}
	config = proto.CloneOf(config)
	moduleLogger, err := log.WithModuleConfig("redis", config.GetLog())
	if err != nil {
		return nil, nil, fmt.Errorf("configure redis logger: %w", err)
	}
	c := &manager{
		Logger:  moduleLogger,
		tracing: tracingProvider,
		metrics: metricsProvider,

		conf:        config,
		connOptions: map[string]connectionOption{},
		connections: map[string]*redis.Client{},
	}

	maps.Copy(c.connOptions, config.GetConnections())

	if err := initializeManager(c, config.GetDefault()); err != nil {
		return nil, nil, err
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if closeErr := c.Close(); closeErr != nil {
				c.With("error", closeErr).Error("redis manager cleanup failed")
			}
		})
	}
	return c, cleanup, nil
}

// initializeManager 初始化默认连接；失败时同步回收已经创建的 client。
func initializeManager(m *manager, defaultName string) (err error) {
	if m == nil {
		return errors.New("redis manager is nil")
	}
	if m.connections == nil {
		m.connections = make(map[string]*redis.Client)
	}
	defer func() {
		if err != nil {
			// 构造失败也必须回收部分 client，否则一次启动失败就会遗留连接池。
			err = errors.Join(err, m.Close())
		}
	}()

	defaultConnection, err := m.Connection(defaultName)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	m.defaultConn = defaultConnection
	m.mu.Unlock()
	return nil
}

// Connection 延迟创建由 Manager 持有的具名 client；调用方不能单独关闭它。
func (m *manager) Connection(name string) (*redis.Client, error) {
	if m == nil {
		return nil, errors.New("redis manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if client, ok := m.connections[name]; ok {
		return client, nil
	}
	// 创建与 Close 串行，保证刚创建的 client 必然被 Manager 接管或立即回收。
	client, err := m.newConnection(name)
	if err != nil {
		if client != nil {
			err = errors.Join(
				err,
				wrapRedisCloseError(name, client.Close()),
			)
		}
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf(
			"create redis connection %q: client is nil",
			name,
		)
	}
	m.connections[name] = client
	return client, nil
}

// Default 返回 Manager 持有的默认 client；关闭后返回 nil。
func (m *manager) Default() *redis.Client {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	return m.defaultConn
}

// newConnection 按只读配置创建 client，并按组件开关安装遥测钩子。
func (m *manager) newConnection(name string) (*redis.Client, error) {
	option := m.connOptions[name]
	if option == nil {
		return nil, fmt.Errorf("redis connection %q option is nil", name)
	}

	options := &redis.Options{
		Network:               option.GetNetwork(),
		Addr:                  option.GetAddr(),
		ClientName:            option.GetClientName(),
		Protocol:              int(option.GetProtocol()),
		Username:              option.GetUsername(),
		Password:              option.GetPassword(),
		DB:                    int(option.GetDb()),
		MaxRetries:            int(option.GetMaxRetries()),
		MinRetryBackoff:       option.GetMinRetryBackoff().AsDuration(),
		MaxRetryBackoff:       option.GetMaxRetryBackoff().AsDuration(),
		DialTimeout:           option.GetDialTimeout().AsDuration(),
		DialerRetries:         int(option.GetDialerRetries()),
		DialerRetryTimeout:    option.GetDialerRetryTimeout().AsDuration(),
		ReadTimeout:           option.GetReadTimeout().AsDuration(),
		WriteTimeout:          option.GetWriteTimeout().AsDuration(),
		ContextTimeoutEnabled: option.GetContextTimeoutEnabled(),
		ReadBufferSize:        int(option.GetReadBufferSize()),
		WriteBufferSize:       int(option.GetWriteBufferSize()),
		PoolFIFO:              option.GetPoolFifo(),
		PoolSize:              int(option.GetPoolSize()),
		MaxConcurrentDials:    int(option.GetMaxConcurrentDials()),
		PoolTimeout:           option.GetPoolTimeout().AsDuration(),
		MinIdleConns:          int(option.GetMinIdleConns()),
		MaxIdleConns:          int(option.GetMaxIdleConns()),
		MaxActiveConns:        int(option.GetMaxActiveConns()),
		ConnMaxIdleTime:       option.GetConnMaxIdleTime().AsDuration(),
		ConnMaxLifetime:       option.GetConnMaxLifetime().AsDuration(),
		DisableIdentity:       option.GetDisableIdentity(),
		IdentitySuffix:        option.GetIdentitySuffix(),
		UnstableResp3:         option.GetUnstableResp3(),
		FailingTimeoutSeconds: int(option.GetFailingTimeoutSeconds()),
	}
	options.Dialer = reconnectDialer(options)
	cc := redis.NewClient(options)
	tracingCfg := m.conf.GetTracing()

	if !tracingCfg.GetDisable() &&
		!m.tracing.Disabled() {
		err := redisotel.InstrumentTracing(cc,
			redisotel.WithTracerProvider(m.tracing.TracerProvider()),
			redisotel.WithDBStatement(tracingCfg.GetDbStatement()),
			redisotel.WithCallerEnabled(tracingCfg.GetCallerEnabled()),
			redisotel.WithDialFilter(tracingCfg.GetDialFilter()),
		)
		if err != nil {
			return cc, fmt.Errorf(
				"instrument redis connection %q tracing: %w",
				name,
				err,
			)
		}
	}

	if !m.conf.GetMetrics().GetDisable() {
		err := redisotel.InstrumentMetrics(cc,
			redisotel.WithMeterProvider(m.metrics.MeterProvider()),
			// 同一地址可配置多个独立连接池，必须保留具名连接身份，避免观测样本冲突。
			redisotel.WithAttributes(attribute.String("redis_connection", name)),
		)
		if err != nil {
			return cc, fmt.Errorf(
				"instrument redis connection %q metrics: %w",
				name,
				err,
			)
		}
	}

	return cc, nil
}

// Close 释放 Manager 创建的全部 client，并聚合关闭错误；重复调用返回同一结果。
func (m *manager) Close() error {
	if m == nil {
		return errors.New("redis manager is nil")
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		names := make([]string, 0, len(m.connections))
		for name := range m.connections {
			names = append(names, name)
		}
		slices.Sort(names)
		clients := make([]*redis.Client, 0, len(names))
		for _, name := range names {
			clients = append(clients, m.connections[name])
		}
		m.connections = nil
		m.defaultConn = nil
		m.mu.Unlock()

		closeErrors := make([]error, 0, len(clients))
		for index, client := range clients {
			if err := wrapRedisCloseError(
				names[index],
				client.Close(),
			); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		m.closeErr = errors.Join(closeErrors...)
	})
	return m.closeErr
}

// wrapRedisCloseError 给关闭错误补充连接名，便于多连接聚合后定位来源。
func wrapRedisCloseError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close redis connection %q: %w", name, err)
}
