package registry

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	kratosregistry "github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Factory 管理具名注册中心实例；返回的能力仅可在 cleanup 前并发借用。
type Factory struct {
	// instances 启动时构造的只读资源表。
	instances map[string]Resource
}

// NewFactory 从最终配置构造全部实例；失败按构造顺序逆序回滚。
func NewFactory(manager config.Manager, logger log.Logger) (*Factory, func(), error) {
	return newFactory(manager, logger, drivers.snapshot())
}

func newFactory(manager config.Manager, logger log.Logger, factories map[string]DriverFactory) (*Factory, func(), error) {
	settings := new(config_pb.Registry)
	if err := manager.Load("registry", settings, new(config_pb.Registry)); err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(settings.Instances))
	for name, instance := range settings.Instances {
		if name == "" || strings.ContainsAny(name, ". \t\n/") || instance == nil {
			return nil, nil, fmt.Errorf("invalid registry instance %q", name)
		}
		if factories[instance.Driver] == nil {
			return nil, nil, fmt.Errorf("registry instance %q: unknown driver %q", name, instance.Driver)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	f := &Factory{instances: make(map[string]Resource, len(names))}
	var cleanups []func()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		})
	}
	for _, name := range names {
		instance := settings.Instances[name]
		resource, closeResource, err := factories[instance.Driver](DriverConfig{Name: name, manager: manager}, logger.With("instance", name))
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("create registry instance %q: %w", name, err)
		}
		if closeResource != nil {
			cleanups = append(cleanups, closeResource)
		}
		if !resource.Disabled && resource.Registrar == nil && resource.Discovery == nil {
			cleanup()
			return nil, nil, fmt.Errorf("registry instance %q provides no capability", name)
		}
		f.instances[name] = resource
	}
	return f, cleanup, nil
}

// Registrar 借用具名注册能力，不转移资源所有权；已禁用实例返回 nil、nil。
func (f *Factory) Registrar(name string) (kratosregistry.Registrar, error) {
	resource, ok := f.instances[name]
	if !ok {
		return nil, fmt.Errorf("registry instance %q not found", name)
	}
	if resource.Disabled {
		return nil, nil
	}
	if resource.Registrar == nil {
		return nil, fmt.Errorf("registry instance %q does not support registration", name)
	}
	return resource.Registrar, nil
}

// Discovery 借用具名发现能力，不转移资源所有权；已禁用实例返回 nil、nil。
func (f *Factory) Discovery(name string) (kratosregistry.Discovery, error) {
	resource, ok := f.instances[name]
	if !ok {
		return nil, fmt.Errorf("registry instance %q not found", name)
	}
	if resource.Disabled {
		return nil, nil
	}
	if resource.Discovery == nil {
		return nil, fmt.Errorf("registry instance %q does not support discovery", name)
	}
	return resource.Discovery, nil
}
