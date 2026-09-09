package oss

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// BucketConfig 是 Manager 传给具体驱动的不可变配置快照。
type BucketConfig struct {
	Name    string
	Bucket  string
	Domain  string
	Options map[string]string
}

// DriverFactory 根据一个 bucket 配置创建驱动实例。
type DriverFactory func(BucketConfig) (Bucket, error)

type driverRegistry struct {
	mu        sync.RWMutex
	factories map[string]DriverFactory
	frozen    bool
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
	return ossDrivers.register(name, factory)
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
