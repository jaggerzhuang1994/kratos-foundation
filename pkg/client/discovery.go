package client

import (
	"fmt"

	"github.com/go-kratos/kratos/v2/registry"
)

// DiscoveryResolver 按配置中的实例名称提供发现能力，资源归提供方所有。
type DiscoveryResolver interface {
	Discovery(string) (registry.Discovery, error)
}

func (b *builder) resolveDiscovery(spec clientSpec) (registry.Discovery, error) {
	if b.discoveries != nil {
		d, err := b.discoveries.Discovery(spec.discovery)
		if err != nil {
			return nil, fmt.Errorf("resolve client %q discovery %q: %w", spec.name, spec.discovery, err)
		}
		if d == nil {
			return nil, ErrDiscoveryNotInitialized
		}
		return d, nil
	}
	return nil, ErrDiscoveryNotInitialized
}
