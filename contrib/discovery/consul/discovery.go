// Package consul 把共享 Consul 客户端适配为 Kratos 服务发现接口。
package consul

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	kratosconsul "github.com/go-kratos/kratos/contrib/registry/consul/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// NewDiscovery 把共享 Consul 客户端适配为 Kratos 发现实现。
//
// nil 客户端表示发现功能已禁用，此时不要求配置管理器存在。返回值不拥有客户端。
func NewDiscovery(
	logger log.Logger,
	config config.Manager,
	client baseconsul.Client,
) (registry.Discovery, error) {
	logger = logger.WithModule("discovery").With("driver", "consul")

	if client == nil {
		logger.Warn("discovery not loaded: consul client not initialized")
		return nil, nil
	}
	componentConfig, err := loadConfig(config)
	if err != nil {
		return nil, err
	}

	return &discovery{client: client, config: componentConfig, logger: logger}, nil
}

type discovery struct {
	client baseconsul.Client
	config discoveryConfig
	logger log.Logger
}

func (d *discovery) GetService(ctx context.Context, name string) ([]*registry.ServiceInstance, error) {
	services, _, err := d.query(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("service %s not resolved in registry", name)
	}
	return services, nil
}

func (d *discovery) Watch(ctx context.Context, name string) (registry.Watcher, error) {
	services, indices, err := d.query(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	// 首次读取成功后才创建工作协程；失败不留下共享缓存或引用计数。
	watchCtx, cancel := context.WithCancel(ctx)
	w := &watcher{events: make(chan []*registry.ServiceInstance, 1), cancel: cancel, done: make(chan struct{})}
	w.events <- services
	go w.run(watchCtx, d, name, indices)
	return w, nil
}

// query 每次返回独立快照，全部 HTTP 请求都受发现超时和调用方取消约束。
func (d *discovery) query(ctx context.Context, name string, previous map[string]uint64) ([]*registry.ServiceInstance, map[string]uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, d.config.timeout)
	defer cancel()
	datacenters := []string{""}
	if d.config.datacenter == kratosconsul.MultiDatacenter {
		// Catalog.Datacenters 没有 context 入口，Raw.Query 沿用相同 SDK 解码和认证路径。
		_, err := d.client.Raw().Query("/v1/catalog/datacenters", &datacenters, (&api.QueryOptions{}).WithContext(ctx))
		if err != nil {
			return nil, nil, err
		}
	}
	services := make([]*registry.ServiceInstance, 0)
	indices := make(map[string]uint64, len(datacenters))
	for _, dc := range datacenters {
		opts := &api.QueryOptions{Datacenter: dc}
		if d.config.datacenter == kratosconsul.SingleDatacenter {
			opts.WaitIndex = previous[dc]
			// Consul 会在阻塞等待上加入抖动，留出请求预算避免正常长轮询超时。
			opts.WaitTime = min(55*time.Second, d.config.timeout/2)
		}
		// 多 DC 使用非阻塞快照，避免前一个 DC 的长轮询饿死后续 DC。
		entries, meta, err := d.client.Health().Service(name, "", true, opts.WithContext(ctx))
		if err != nil {
			return nil, nil, err
		}
		indices[dc] = max(meta.LastIndex, 1)
		if meta.LastIndex < previous[dc] {
			// Consul 快照恢复可能使 index 回退，下一次必须取消阻塞并重新取快照。
			indices[dc] = 0
		}
		for _, entry := range entries {
			if entry == nil || entry.Service == nil {
				continue
			}
			service := serviceInstance(entry.Service)
			if d.config.datacenter == kratosconsul.MultiDatacenter {
				if service.Metadata == nil {
					service.Metadata = make(map[string]string)
				}
				service.Metadata["dc"] = dc
			}
			services = append(services, service)
		}
	}
	return services, indices, nil
}

// serviceInstance 保留 Kratos Consul 的标签和端点映射，并稳定 map 端点顺序。
func serviceInstance(service *api.AgentService) *registry.ServiceInstance {
	var version string
	for _, tag := range service.Tags {
		if value, ok := strings.CutPrefix(tag, "version="); ok {
			version = value
		}
	}
	endpoints := make([]string, 0, len(service.TaggedAddresses))
	for scheme, address := range service.TaggedAddresses {
		switch scheme {
		case "lan_ipv4", "wan_ipv4", "lan_ipv6", "wan_ipv6":
			continue
		}
		endpoints = append(endpoints, address.Address)
	}
	slices.Sort(endpoints)
	if len(endpoints) == 0 && service.Address != "" && service.Port != 0 {
		endpoints = append(endpoints, "http://"+net.JoinHostPort(service.Address, strconv.Itoa(service.Port)))
	}
	return &registry.ServiceInstance{ID: service.ID, Name: service.Service, Version: version, Metadata: service.Meta, Endpoints: endpoints}
}
