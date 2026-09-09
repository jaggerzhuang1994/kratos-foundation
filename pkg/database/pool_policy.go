package database

import (
	"fmt"
	"sync"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// poolFieldNames 是可以在运行期直接写回 *sql.DB 的连接池参数。按字段名清理后比较其余配置。
var poolFieldNames = []protoreflect.Name{
	"max_idle_conns",
	"max_open_conns",
	"conn_max_lifetime",
	"conn_max_idle_time",
}

// subscribeConnectionPools 订阅 database 段并只应用连接池参数的变化。
//
// 这四个参数对应 *sql.DB 的 SetMaxIdleConns 等 setter，可在任意时刻并发安全地调用，
// 调整它们不需要重连数据库，因此连接池打满时可以在线扩容。dsn、driver、连接集合
// 以及 GORM、AES、日志等配置决定连接与会话本身，无法在不重建 Manager 的情况下切换，
// 所以这里只在检测到这类改动时告警，不做部分应用。
func subscribeConnectionPools(
	configManager foundationconfig.Manager,
	logger log.Logger,
	initial *config_pb.Database,
	factory *connectionFactory,
	drivers map[string]DriverFactory,
) (func(), error) {
	// 连接身份始终对应启动时的资源。比较时忽略池参数，且不让任何后续
	// 快照推进基线，避免被拒绝的拓扑变更在下一次更新时绕过检查。
	startupConfig := proto.CloneOf(initial)
	cancel, err := configManager.Subscribe(
		"database",
		new(config_pb.Database),
		func(_ string, value any, updateErr error) {
			next, valid := value.(*config_pb.Database)
			if updateErr == nil && (!valid || next == nil) {
				updateErr = fmt.Errorf(
					"database config update has type %T, want *config_pb.Database",
					value,
				)
			}
			if updateErr == nil {
				updateErr = validateDatabaseConfig(next, drivers)
			}
			if updateErr != nil {
				logger.With("error", updateErr).Error("subscribeConnectionPools | database config update rejected")
				return
			}
			if !poolOnlyChange(startupConfig, next) {
				// 连接身份变化后不可把新池参数写回启动时的另一份资源。
				logger.Warn(
					"subscribeConnectionPools | database hot update skipped: changes to dsn, driver or " +
						"plugin config require a restart, and only connection pool sizing " +
						"can be applied at runtime",
				)
				return
			}
			applied := applyConnectionPools(factory, next)
			if applied > 0 {
				logger.With("connections", applied).Info(
					"subscribeConnectionPools | database connection pool config updated",
				)
			}
		},
		defaultConfig(),
	)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(cancel) }, nil
}

// applyConnectionPools 把最新的连接池参数写入已经建立的连接，返回实际更新的数量。
// 配置里新增的连接在本进程中没有对应连接池，只能由重启创建，因此这里直接跳过。
func applyConnectionPools(factory *connectionFactory, config *config_pb.Database) int {
	applied := 0
	for name, connection := range config.GetConnections() {
		if connection == nil {
			continue
		}
		if pool, ok := factory.pool(name); ok {
			configConnectionPool(pool.db, connection)
			applied++
		}

	}
	return applied
}

// poolOnlyChange 报告两份配置是否只有连接池参数不同。
func poolOnlyChange(current, next *config_pb.Database) bool {
	return proto.Equal(withoutPoolFields(current), withoutPoolFields(next))
}

// withoutPoolFields 返回清空全部连接池参数后的副本，用于比较其余字段。
func withoutPoolFields(config *config_pb.Database) *config_pb.Database {
	clone := proto.CloneOf(config)
	for _, connection := range clone.GetConnections() {
		if connection == nil {
			continue
		}
		clearPoolFields(connection)

	}
	return clone
}

// clearPoolFields 清空一份连接配置中的全部连接池参数。
func clearPoolFields(message proto.Message) {
	reflection := message.ProtoReflect()
	fields := reflection.Descriptor().Fields()
	for _, name := range poolFieldNames {
		if field := fields.ByName(name); field != nil {
			reflection.Clear(field)
		}
	}
}
