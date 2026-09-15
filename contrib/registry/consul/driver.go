package consul

import (
	"context"
	"time"

	"errors"
	"fmt"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
)

func init() {
	if err := registry.RegisterDriver("consul", newDriver); err != nil {
		panic(err)
	}
}

func newDriver(settings registry.DriverConfig, logger log.Logger) (registry.Resource, func(), error) {
	// 旧连接配置必须报错，防止调用方以为具名实例仍能连接不同集群。
	var removed any
	if err := settings.Load("connection", &removed); err == nil {
		return registry.Resource{}, nil, fmt.Errorf("consul options.connection was removed; configure the process client through env")
	} else if !errors.Is(err, config.ErrNotFound) {
		return registry.Resource{}, nil, err
	}
	client, disabled, err := baseconsul.Get()
	if err != nil {
		return registry.Resource{}, nil, err
	}
	if disabled {
		return registry.Resource{Disabled: true}, nil, nil
	}
	registration, err := newRegistrar(logger, settings, client)
	if err != nil {
		return registry.Resource{}, nil, err
	}
	discovery, err := newDiscovery(logger, settings, client)
	if err != nil {
		return registry.Resource{}, nil, err
	}
	return registry.Resource{Registrar: registration, Discovery: discovery}, func() {
		// 正常退出先由 App 注销；构造回滚时也必须取消残留心跳。
		r := registration
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.acquire(ctx); err != nil {
			logger.With("function", "newDriver.cleanup", "error", err).Error("acquire registrar cleanup failed")
		} else {
			for _, active := range r.services {
				active.cancel()
			}
			for _, active := range r.services {
				<-active.done
			}
			clear(r.services)
			<-r.operations
		}
	}, nil
}
