// Package consul 把共享 Consul 客户端适配为 Kratos 服务注册接口。
package consul

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// NewRegistry 把共享 Consul 客户端适配为 Kratos 注册实现。
//
// nil 客户端表示注册功能已禁用，此时不要求配置管理器存在。返回值不拥有客户端。
func NewRegistry(
	logger log.Logger,
	config config.Manager,
	client baseconsul.Client,
) (registry.Registrar, error) {
	logger = logger.WithModule("registry").With("driver", "consul")

	if client == nil {
		logger.Warn("registry not loaded: consul client not initialized")
		return nil, nil
	}
	componentConfig, err := loadConfig(config)
	if err != nil {
		return nil, err
	}

	return &registrar{client: client, config: componentConfig, logger: logger,
		operations: make(chan struct{}, 1), services: make(map[string]*registration)}, nil
}

const requestTimeout = 10 * time.Second

type registration struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type registrar struct {
	client baseconsul.Client
	config registryConfig
	logger log.Logger
	// 生命周期操作在实例内串行化，等待可被 ctx 取消；后台心跳不访问此 map。
	operations chan struct{}
	services   map[string]*registration
}

func (r *registrar) Register(ctx context.Context, service *registry.ServiceInstance) error {
	if service == nil {
		return errors.New("consul registration service is nil")
	}
	payload, err := r.serviceRegistration(service)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err = r.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-r.operations }()
	if err = r.stop(ctx, service.ID); err != nil {
		return err
	}
	if err = r.client.Agent().ServiceRegisterOpts(payload, api.ServiceRegisterOpts{}.WithContext(ctx)); err != nil {
		return err
	}
	if !r.config.disableHeartbeat {
		// Register 的调用超时只约束登记请求；后台所有权一直持续到 Deregister。
		heartbeatCtx, stop := context.WithCancel(context.Background())
		active := &registration{cancel: stop, done: make(chan struct{})}
		r.services[service.ID] = active
		go func() {
			defer close(active.done)
			defer stop()
			r.heartbeat(heartbeatCtx, payload)
		}()
	}
	return nil
}

func (r *registrar) Deregister(ctx context.Context, service *registry.ServiceInstance) error {
	if service == nil {
		return errors.New("consul deregistration service is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := r.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-r.operations }()
	// 先终止续报/重登记再远程注销，避免后台把已注销服务重新创建。
	if err := r.stop(ctx, service.ID); err != nil {
		return err
	}
	err := r.client.Agent().ServiceDeregisterOpts(service.ID, (&api.QueryOptions{}).WithContext(ctx))
	var status api.StatusError
	if errors.As(err, &status) && status.Code == 404 {
		return nil
	}
	return err
}

func (r *registrar) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case r.operations <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *registrar) stop(ctx context.Context, id string) error {
	active := r.services[id]
	if active == nil {
		return nil
	}
	active.cancel()
	select {
	case <-active.done:
		delete(r.services, id)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// serviceRegistration 保留 SDK 的端点和检查映射，持有独立快照用于缺失后的重新登记。
func (r *registrar) serviceRegistration(service *registry.ServiceInstance) (*api.AgentServiceRegistration, error) {
	addresses := make(map[string]api.ServiceAddress, len(service.Endpoints))
	checks := make([]string, 0, len(service.Endpoints))
	for _, endpoint := range service.Endpoints {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		var port uint64
		if text := parsed.Port(); text != "" {
			// 拒绝溢出，避免 ParseUint 的饱和值悄悄改变健康检查目标。
			port, err = strconv.ParseUint(text, 10, 16)
			if err != nil {
				return nil, fmt.Errorf("invalid service endpoint port %q: %w", text, err)
			}
		}
		checks = append(checks, net.JoinHostPort(parsed.Hostname(), strconv.FormatUint(port, 10)))
		addresses[parsed.Scheme] = api.ServiceAddress{Address: endpoint, Port: int(port)}
	}
	payload := &api.AgentServiceRegistration{
		ID: service.ID, Name: service.Name, Meta: maps.Clone(service.Metadata),
		Tags: append([]string{"version=" + service.Version}, r.config.tags...), TaggedAddresses: addresses,
	}
	if len(checks) > 0 {
		host, port, _ := net.SplitHostPort(checks[0])
		payload.Address = host
		payload.Port, _ = strconv.Atoi(port)
	}
	critical := fmt.Sprintf("%ds", r.config.deregisterCriticalAfterSeconds)
	if !r.config.disableHealthCheck {
		for _, address := range checks {
			payload.Checks = append(payload.Checks, &api.AgentServiceCheck{
				TCP: address, Interval: fmt.Sprintf("%ds", r.config.healthCheckIntervalSeconds),
				DeregisterCriticalServiceAfter: critical, Timeout: "5s",
			})
		}
	}
	if !r.config.disableHeartbeat {
		payload.Checks = append(payload.Checks, &api.AgentServiceCheck{
			CheckID: "service:" + service.ID, TTL: fmt.Sprintf("%ds", r.config.healthCheckIntervalSeconds*2),
			DeregisterCriticalServiceAfter: critical,
		})
	}
	return payload, nil
}
