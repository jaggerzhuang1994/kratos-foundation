package database

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"gorm.io/gorm"
)

var errConnectionFactoryClosed = errors.New("database connection factory is closed")

// connectionPool 记录受管 SQL 连接池及其观测标识。
type connectionPool struct {
	// db 由连接工厂负责关闭的 SQL 连接池。
	db *sql.DB
	// name 用于指标标签的逻辑连接名。
	name string
	// driver 用于选择专用采集器的驱动名。
	driver string
}

type connectionFactory struct {
	// mu 保护连接登记和关闭状态。
	mu sync.Mutex
	// drivers 构造时固定的驱动工厂快照。
	drivers map[string]DriverFactory
	// connections 按连接池指针登记的受管资源，受 mu 保护。
	connections map[*sql.DB]connectionPool
	// closed 是否已停止接收新连接，受 mu 保护。
	closed bool
	// closeOnce 保证连接池仅清理一次。
	closeOnce sync.Once
	// closeErr 首次清理的聚合错误，后续关闭复用该结果。
	closeErr error
}

// newConnectionFactory 创建使用固定驱动快照的 SQL 连接池跟踪器。
func newConnectionFactory(drivers map[string]DriverFactory) *connectionFactory {
	return &connectionFactory{
		drivers:     drivers,
		connections: make(map[*sql.DB]connectionPool),
	}
}

// make 创建并纳管一个连接池；工厂关闭后不再接受新连接。
func (f *connectionFactory) make(
	name string,
	conf *config_pb.DBConnection,
) (gorm.Dialector, error) {
	if conf == nil {
		return nil, errors.New("database connection config is nil")
	}

	driver := normalizeDriverName(conf.GetDriver())
	if driver == "" {
		driver = "mysql"
	}
	factory, ok := f.drivers[driver]
	if !ok {
		return nil, fmt.Errorf("unsupported database driver %q", conf.GetDriver())
	}

	connection, err := factory(DriverConfig{Name: name, DSN: conf.GetDsn()})
	if err != nil {
		// todo connection 可能为空
		if connection.SQLDB != nil {
			err = errors.Join(err, connection.SQLDB.Close())
		}
		return nil, fmt.Errorf("open %s database connection: %w", driver, err)
	}
	if connection.SQLDB == nil {
		return nil, fmt.Errorf("database driver %q returned a nil SQL DB", driver)
	}
	if connection.Dialector == nil {
		return nil, errors.Join(
			fmt.Errorf("database driver %q returned a nil GORM dialector", driver),
			connection.SQLDB.Close(),
		)
	}

	configConnectionPool(connection.SQLDB, conf)
	if err = f.track(connectionPool{db: connection.SQLDB, name: name, driver: driver}); err != nil {
		return nil, errors.Join(err, connection.SQLDB.Close())
	}
	return connection.Dialector, nil
}

// track 在同一把锁下判断关闭状态并登记连接，避免关闭期间泄漏新连接。
func (f *connectionFactory) track(pool connectionPool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errConnectionFactoryClosed
	}
	if f.connections == nil {
		f.connections = make(map[*sql.DB]connectionPool)
	}
	f.connections[pool.db] = pool
	return nil
}

// pools 返回按逻辑名排序的连接池快照，使指标注册与错误稳定可重现。
func (f *connectionFactory) pools() []connectionPool {
	f.mu.Lock()
	defer f.mu.Unlock()
	pools := make([]connectionPool, 0, len(f.connections))
	for _, pool := range f.connections {
		pools = append(pools, pool)
	}
	slices.SortFunc(pools, func(a, b connectionPool) int {
		return cmp.Compare(a.name, b.name)
	})
	return pools
}

// pool 按逻辑名返回连接池快照，仅用于组件构造期的精确选择。
func (f *connectionFactory) pool(name string) (connectionPool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pool := range f.connections {
		if pool.name == name {
			return pool, true
		}
	}
	return connectionPool{}, false
}

// close 按 Manager 的具名连接构造顺序逆序、幂等关闭全部连接池。
func (f *connectionFactory) close() error {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closed = true
		connections := make([]connectionPool, 0, len(f.connections))
		for _, connection := range f.connections {
			connections = append(connections, connection)
		}
		f.connections = nil
		f.mu.Unlock()
		slices.SortFunc(connections, func(a, b connectionPool) int {
			return cmp.Compare(b.name, a.name)
		})

		closeErrors := make([]error, 0, len(connections))
		for _, connection := range connections {
			if err := connection.db.Close(); err != nil {
				closeErrors = append(
					closeErrors,
					fmt.Errorf(
						"close database connection %q: %w",
						connection.name,
						err,
					),
				)
			}
		}
		f.closeErr = errors.Join(closeErrors...)
	})
	return f.closeErr
}

// closeConnectionFactoryAfterError 在构造失败时同时保留业务错误与连接关闭错误。
func closeConnectionFactoryAfterError(factory *connectionFactory, err error) error {
	return errors.Join(err, factory.close())
}

// configConnectionPool 应用完整池配置，删除字段时恢复与新建连接池一致的默认值。
func configConnectionPool(cp *sql.DB, conf *config_pb.DBConnection) {
	maxIdle := int(conf.GetMaxIdleConns())
	if conf.MaxIdleConns == nil {
		maxIdle = 2 // 沿用 database/sql 的默认值；显式 0 仍表示不保留空闲连接。
	}
	// sql.DB 会用当前 max open 截断 max idle，扩容时必须先更新连接总上限。
	cp.SetMaxOpenConns(int(conf.GetMaxOpenConns()))
	cp.SetMaxIdleConns(maxIdle)
	// protobuf 的 AsDuration 对 nil 返回 0，表示取消对应的过期限制。
	cp.SetConnMaxLifetime(conf.GetConnMaxLifetime().AsDuration())
	cp.SetConnMaxIdleTime(conf.GetConnMaxIdleTime().AsDuration())
}

func validateConnections(config *config_pb.Database, drivers map[string]DriverFactory) error {
	if config == nil {
		return fmt.Errorf("database config is nil")
	}
	connections := config.GetConnections()
	if _, ok := connections[config.GetDefault()]; !ok {
		return fmt.Errorf("database default connection %q is not configured", config.GetDefault())
	}
	for _, name := range slices.Sorted(maps.Keys(connections)) {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("database connection name is required")
		}
		if strings.TrimSpace(name) != name {
			return fmt.Errorf("database connection name %q has surrounding whitespace", name)
		}
		if connections[name] == nil {
			return fmt.Errorf("database connection %q is nil", name)
		}
		if err := validateConnectionConfig(fmt.Sprintf("database connection %q", name), connections[name], drivers); err != nil {
			return err
		}
	}
	return nil
}

func validateConnectionConfig(
	location string,
	config *config_pb.DBConnection,
	drivers map[string]DriverFactory,
) error {
	driver := normalizeDriverName(config.GetDriver())
	if driver == "" {
		driver = "mysql"
	}
	if _, exists := drivers[driver]; !exists {
		return fmt.Errorf("%s has unsupported driver %q", location, config.GetDriver())
	}
	if strings.TrimSpace(config.GetDsn()) == "" {
		return fmt.Errorf("%s DSN is required", location)
	}
	return nil
}
