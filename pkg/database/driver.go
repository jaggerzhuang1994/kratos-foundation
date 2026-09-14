package database

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"gorm.io/gorm"
)

// DriverConfig 是数据库驱动工厂创建连接所需的最小配置。
type DriverConfig struct {
	Name string
	DSN  string
}

// DriverConnection 是数据库驱动工厂返回的 SQL 连接与 GORM 方言组合。
type DriverConnection struct {
	SQLDB     *sql.DB
	Dialector gorm.Dialector
}

// DriverFactory 根据配置创建数据库连接；调用方负责接管并关闭 SQLDB。
type DriverFactory func(DriverConfig) (DriverConnection, error)

type driverRegistry struct {
	mu        sync.RWMutex
	factories map[string]DriverFactory
	frozen    bool
}

var databaseDrivers = newDriverRegistry()

func newDriverRegistry() *driverRegistry {
	return &driverRegistry{factories: make(map[string]DriverFactory)}
}

func normalizeDriverName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func (r *driverRegistry) register(name string, factory DriverFactory) error {
	name = normalizeDriverName(name)
	if name == "" {
		return errors.New("database driver name is empty")
	}
	if factory == nil {
		return fmt.Errorf("database driver %q factory is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return errors.New("database driver registry is frozen")
	}
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("database driver %q is already registered", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *driverRegistry) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (r *driverRegistry) snapshotAndFreeze() map[string]DriverFactory {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	snapshot := make(map[string]DriverFactory, len(r.factories))
	maps.Copy(snapshot, r.factories)
	return snapshot
}

// RegisterDriver 注册数据库驱动工厂。首次构造 Manager 后注册表会被冻结。
func RegisterDriver(name string, factory DriverFactory) error {
	if err := databaseDrivers.register(name, factory); err != nil {
		return err
	}
	// register 已释放注册表锁，日志输出不会阻塞锁内注册与查询。
	log.WithModule("database").With("function", "RegisterDriver", "driver", normalizeDriverName(name)).Info("Registered database driver")
	return nil
}

// MustRegisterDriver 注册数据库驱动工厂，注册失败时 panic，适合驱动包的 init 使用。
func MustRegisterDriver(name string, factory DriverFactory) {
	if err := RegisterDriver(name, factory); err != nil {
		panic(err)
	}
}

// RegisteredDrivers 返回已注册驱动名称的有序副本。
func RegisteredDrivers() []string {
	return databaseDrivers.names()
}

func driverSnapshot() map[string]DriverFactory {
	return databaseDrivers.snapshotAndFreeze()
}
