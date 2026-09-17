package oss

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// BucketConfig 是 Manager 为每次驱动构造提供的独立配置快照。
type BucketConfig struct {
	// Name 应用使用的逻辑 bucket 名。
	Name string
	// Bucket 存储服务中的实际 bucket 名。
	Bucket string
	// Domain 可选的绝对 HTTP(S) 公开访问地址，可带基础路径，不是签名 URL。
	Domain string
	// Options 驱动专属配置的独立副本，可能包含凭据，不应整体记录。
	Options map[string]string
}

// DriverFactory 根据一个 bucket 配置创建驱动实例。
// 不同逻辑 bucket 可并发调用 factory；实现应并发安全并自行限制外部调用时长。
type DriverFactory func(BucketConfig) (Bucket, error)

type driverRegistry struct {
	// mu 保护工厂注册表和冻结状态。
	mu sync.RWMutex
	// factories 按驱动名称保存的无状态工厂。
	factories map[string]DriverFactory
	// frozen 取得快照后禁止继续注册。
	frozen bool
}

var ossDrivers = newDriverRegistry()

func newDriverRegistry() *driverRegistry {
	return &driverRegistry{factories: make(map[string]DriverFactory)}
}

func (r *driverRegistry) register(name string, factory DriverFactory) error {
	name = normalizeDriverName(name)
	if name == "" {
		return errors.New("OSS driver name is empty")
	}
	if factory == nil {
		return fmt.Errorf("OSS driver %q factory is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return errors.New("OSS driver registry is frozen")
	}
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("OSS driver %q is already registered", name)
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

// RegisterDriver 注册全局唯一的对象存储驱动工厂。
func RegisterDriver(name string, factory DriverFactory) error {
	if err := ossDrivers.register(name, factory); err != nil {
		return err
	}
	// register 已释放注册表锁，日志输出不会阻塞锁内注册与查询。
	log.WithModule("oss").With("driver", normalizeDriverName(name)).Info("Registered OSS driver")
	return nil
}

// MustRegisterDriver 注册驱动，失败时 panic，适合 contrib 包的 init 阶段。
func MustRegisterDriver(name string, factory DriverFactory) {
	if err := RegisterDriver(name, factory); err != nil {
		panic(err)
	}
}

// RegisteredDrivers 返回已注册驱动名的有序快照。
func RegisteredDrivers() []string {
	return ossDrivers.names()
}

// driverSnapshot 冻结并复制当前注册表，使 Manager 的行为保持稳定。
func driverSnapshot() map[string]DriverFactory {
	return ossDrivers.snapshotAndFreeze()
}

// normalizeDriverName 将驱动名标准化为小写形式。
func normalizeDriverName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
