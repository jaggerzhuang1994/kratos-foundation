package redis

import (
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/metric"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/tracing"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

type Manager struct {
	log     *log.Helper
	tracing *tracing.Tracing
	metrics *metric.Metrics

	conf        *Config
	connOptions map[string]*RedisOption

	// conn store & init locker
	conn   sync.Map
	locker sync.Mutex

	// default redis connection
	defaultConn *Client
}

func NewManager(
	log *log.Log,
	conf *Config,
	tracing *tracing.Tracing,
	metrics *metric.Metrics,
) (*Manager, func(), error) {
	c := &Manager{
		log:     log.WithModule("redis", conf.GetLog()).NewHelper(),
		tracing: tracing,
		metrics: metrics,

		conf:        conf,
		connOptions: map[string]*RedisOption{},
	}

	for name, option := range conf.GetConnections() {
		c.connOptions[name] = option
	}

	defaultConnection, err := c.GetConnection(conf.GetDefault())
	if err != nil {
		return nil, nil, err
	}

	c.defaultConn = defaultConnection

	return c, func() {
		c.release(0)
	}, nil
}

func (m *Manager) GetConnection(conn string) (*Client, error) {
	// 如果已经初始化，则直接返回
	if rds, ok := m.conn.Load(conn); ok {
		return rds.(*Client), nil
	}
	// 开始初始化
	m.locker.Lock()
	defer m.locker.Unlock()

	// 如果两个协程同时 lock，有一个初始化完，另一个则判断是否有初始化的链接，防止重复初始化
	if rds, ok := m.conn.Load(conn); ok {
		return rds.(*Client), nil
	}
	// 开始初始化
	rds, err := m.newConnection(conn)
	if err != nil {
		return nil, err
	}
	m.conn.Store(conn, rds)
	return rds, nil
}

func (m *Manager) newConnection(name string) (*Client, error) {
	option := m.connOptions[name]
	if option == nil {
		return nil, errors.Errorf("redis connection [%s] option is nil", name)
	}

	cc := redis.NewClient(&redis.Options{
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
		DisableIndentity:      option.GetDisableIndentity(),
		DisableIdentity:       option.GetDisableIdentity(),
		IdentitySuffix:        option.GetIdentitySuffix(),
		UnstableResp3:         option.GetUnstableResp3(),
		FailingTimeoutSeconds: int(option.GetFailingTimeoutSeconds()),
	})
	tracingCfg := m.conf.GetTracing()

	if !tracingCfg.GetDisable() {
		err := redisotel.InstrumentTracing(cc,
			redisotel.WithTracerProvider(m.tracing.GetTracerProvider()),
			redisotel.WithDBStatement(tracingCfg.GetDbStatement()),
			redisotel.WithCallerEnabled(tracingCfg.GetCallerEnabled()),
			redisotel.WithDialFilter(tracingCfg.GetDialFilter()),
		)
		if err != nil {
			m.log.Warn("redisotel.InstrumentTracing error", err)
		}
	}

	if !m.conf.GetMetrics().GetDisable() {
		err := redisotel.InstrumentMetrics(cc, redisotel.WithMeterProvider(m.metrics.GetMeterProvider()))
		if err != nil {
			m.log.Warn("redisotel.InstrumentMetrics error", err)
		}
	}

	return cc, nil
}

func (m *Manager) release(after time.Duration) {
	// 释放所有连接
	m.log.Info("release all redis connections, after ", after)
	m.conn.Range(func(key, value any) bool {
		if after > 0 {
			go func() {
				time.Sleep(after)
				_ = value.(*Client).Close()
			}()
		} else {
			_ = value.(*Client).Close()
		}
		m.conn.Delete(key)
		return true
	})
}
