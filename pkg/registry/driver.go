// Package registry 管理具名服务注册和发现驱动。
package registry

import (
	"fmt"
	"maps"
	"strings"
	"sync"

	kratosregistry "github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Resource 表示同一注册中心实例提供的能力。
type Resource struct {
	// Disabled 表示整个实例显式禁用，此时不提供注册与发现能力。
	Disabled bool
	// Registrar 服务注册能力；不支持时为 nil，由工厂 cleanup 释放。
	Registrar kratosregistry.Registrar
	// Discovery 服务发现能力；不支持时为 nil，由工厂 cleanup 释放。
	Discovery kratosregistry.Discovery
}

// DriverConfig 只允许读取当前实例的 options，驱动不得订阅并替换实例。
type DriverConfig struct {
	// Name 当前注册中心实例的逻辑名称。
	Name string
	// manager 供驱动加载本实例配置的配置管理器。
	manager config.Manager
}

// Load 读取当前实例 options 下的配置；默认值规则与 config.Manager.Load 相同。
func (c DriverConfig) Load(key string, target any, defaults ...any) error {
	return c.manager.Load("registry.instances."+c.Name+".options."+key, target, defaults...)
}

// DriverFactory 构造独立资源并返回 cleanup；失败前也必须释放自身已创建的资源。
// cleanup 由组装层在 App 注销和客户端监听停止后调用。
type DriverFactory func(DriverConfig, log.Logger) (Resource, func(), error)

type driverRegistry struct {
	// mu 保护工厂注册表和冻结状态。
	mu sync.Mutex
	// factories 按驱动名称保存的无状态工厂。
	factories map[string]DriverFactory
	// frozen 取得快照后禁止继续注册。
	frozen bool
}

var drivers = driverRegistry{factories: make(map[string]DriverFactory)}

// RegisterDriver 注册无状态工厂。仅在 init 阶段调用，首次构造后拒绝注册。
func RegisterDriver(name string, factory DriverFactory) error {
	return drivers.register(name, factory)
}

func (r *driverRegistry) register(name string, factory DriverFactory) error {
	if name == "" || strings.TrimSpace(name) != name || factory == nil {
		return fmt.Errorf("invalid registry driver %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("registry drivers are frozen")
	}
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("registry driver %q already registered", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *driverRegistry) snapshot() map[string]DriverFactory {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	return maps.Clone(r.factories)
}
