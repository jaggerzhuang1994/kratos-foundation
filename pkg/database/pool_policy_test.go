package database

import (
	"fmt"
	"testing"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func TestPoolPolicyClearsOnlyPoolFieldsAndDetectsChanges(t *testing.T) {
	current := &config_pb.Database{Default: proto.String("default"), Connections: map[string]*config_pb.DBConnection{"default": {Driver: proto.String("mysql"), Dsn: "dsn", MaxIdleConns: proto.Int32(1), MaxOpenConns: proto.Int32(2)}}}
	next := proto.CloneOf(current)
	next.Connections["default"].MaxIdleConns = proto.Int32(9)
	if !poolOnlyChange(current, next) {
		t.Fatal("pool-only update was rejected")
	}
	next.Connections["default"].Dsn = "other"
	if poolOnlyChange(current, next) {
		t.Fatal("identity update was accepted as pool-only")
	}
	cleared := withoutPoolFields(current)
	if cleared.Connections["default"].MaxIdleConns != nil || cleared.Connections["default"].MaxOpenConns != nil {
		t.Fatalf("pool fields remain: %#v", cleared.Connections["default"])
	}
	if current.Connections["default"].GetMaxIdleConns() != 1 {
		t.Fatal("input config mutated")
	}

}

// poolPolicyConfig 在测试线程内回放与投递配置，避免用时间等待推断更新是否已完成。
type poolPolicyConfig struct {
	foundationconfig.Manager
	initial  *config_pb.Database
	observer foundationconfig.Observer
}

func (m *poolPolicyConfig) Subscribe(
	key string,
	_ any,
	observer foundationconfig.Observer,
	_ ...any,
) (func(), error) {
	m.observer = observer
	observer(key, proto.CloneOf(m.initial), nil)
	return func() { m.observer = nil }, nil
}

func TestPoolUpdatesStayBoundToStartupTopology(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*config_pb.DBConnection)
	}{
		{"dsn changed", func(connection *config_pb.DBConnection) {
			connection.Dsn = "file:replacement?mode=memory&cache=shared"
		}},
		{"GORM changed", func(connection *config_pb.DBConnection) {
			connection.Gorm = &config_pb.Gorm{QueryFields: proto.Bool(true)}
		}},
		{"AES changed", func(connection *config_pb.DBConnection) {
			connection.Aes = &config_pb.DatabaseAes{Key: proto.String("MDEyMzQ1Njc4OWFiY2RlZg==")}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			initial := defaultConfig()
			initial.Connections = map[string]*config_pb.DBConnection{
				"default": {
					Driver: proto.String("sqlite3"), Dsn: ":memory:", MaxOpenConns: proto.Int32(1),
				},
			}
			driver := &recordingSQLiteDriver{}
			drivers := map[string]DriverFactory{"sqlite3": driver.open}
			factory := newConnectionFactory(drivers)
			t.Cleanup(func() {
				if err := factory.close(); err != nil {
					t.Error(err)
				}
			})
			connection := initial.Connections["default"]
			if _, err := factory.make("default", connection); err != nil {
				t.Fatal(err)
			}

			manager := &poolPolicyConfig{initial: initial}
			logger := &poolPolicyLogger{Logger: newManagerTestLogger(t)}
			cancel, err := subscribeConnectionPools(manager, logger, initial, factory, drivers)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cancel)
			if len(logger.messages) != 0 {
				t.Fatalf("initial replay logged an update: %v", logger.messages)
			}

			withLimits := func(config *config_pb.Database, base int32) *config_pb.Database {
				next := proto.CloneOf(config)
				connection := next.Connections["default"]
				connection.MaxOpenConns = proto.Int32(base)

				return next
			}
			assertLimits := func(base int) {
				t.Helper()
				for index, pool := range factory.pools() {
					if got, want := pool.db.Stats().MaxOpenConnections, base+index; got != want {
						t.Errorf("pool %q max open = %d, want %d", pool.name, got, want)
					}
				}
			}

			// 先成功热更新，确保拒绝后保留的是正在使用的池参数。
			manager.observer("database", withLimits(initial, 10), nil)
			assertLimits(10)
			if len(logger.messages) != 1 {
				t.Fatalf("update logs=%v", logger.messages)
			}
			manager.observer("database", withLimits(initial, 10), nil)
			if len(logger.messages) != 1 {
				t.Fatal("identical notification logged another update")
			}
			changed := withLimits(initial, 20)
			tt.change(changed.Connections["default"])
			if err := validateDatabaseConfig(changed, drivers); err != nil {
				t.Fatalf("topology change must pass structural validation: %v", err)
			}
			manager.observer("database", changed, nil)
			assertLimits(10)
			// 新拓扑未生效，其后连续的池参数变化也不能应用到旧连接。
			manager.observer("database", withLimits(changed, 30), nil)
			assertLimits(10)
			manager.observer("database", withLimits(changed, 40), nil)
			assertLimits(10)

			// 恢复真实启动拓扑后应继续支持更新，不能因一次拒绝永久停止热更新。
			manager.observer("database", withLimits(initial, 50), nil)
			assertLimits(50)
			manager.observer("database", withLimits(initial, 60), nil)
			assertLimits(60)
			manager.observer("database", proto.CloneOf(initial), nil)
			assertLimits(1)
			if len(logger.messages) != 4 {
				t.Fatalf("restore to initial configuration must log a real update: %v", logger.messages)
			}
		})
	}
}

// poolPolicyLogger 仅捕获更新提示，错误和警告沿用测试 Logger。
type poolPolicyLogger struct {
	log.Logger
	messages []string
}

func (l *poolPolicyLogger) With(...any) log.Logger { return l }
func (l *poolPolicyLogger) Info(values ...any) {
	l.messages = append(l.messages, fmt.Sprint(values...))
}
