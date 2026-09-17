package consul

import (
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"time"

	kratosconsul "github.com/go-kratos/kratos/contrib/registry/consul/v2"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type discoveryConfig struct {
	// timeout 指定一次发现查询的总超时，默认 10 秒，必须为正数。
	timeout time.Duration
	// datacenter 指定单数据中心或跨数据中心发现策略，默认单数据中心。
	datacenter kratosconsul.Datacenter
}

// loadDiscoveryConfig 一次完成发现配置的默认值合并、protobuf 校验和业务语义归一化。
func loadDiscoveryConfig(config config.Reader) (discoveryConfig, error) {
	if config == nil {
		return discoveryConfig{}, errors.New("consul discovery config manager is nil")
	}
	datacenter := config_pb.DC_SINGLE
	defaults := &config_pb.Discovery{
		Timeout: durationpb.New(10 * time.Second),
		Dc:      &datacenter,
	}
	effective := new(config_pb.Discovery)
	if err := config.Load("discovery", effective, defaults); err != nil {
		return discoveryConfig{}, fmt.Errorf("load Consul discovery config: %w", err)
	}
	if effective.Timeout == nil {
		return discoveryConfig{}, errors.New("consul discovery timeout is required")
	}
	if err := effective.Timeout.CheckValid(); err != nil {
		return discoveryConfig{}, fmt.Errorf("consul discovery timeout: %w", err)
	}
	timeout := effective.Timeout.AsDuration()
	if timeout <= 0 {
		return discoveryConfig{}, errors.New("consul discovery timeout must be positive")
	}
	if effective.Dc == nil {
		return discoveryConfig{}, errors.New("consul discovery datacenter is required")
	}
	var normalizedDatacenter kratosconsul.Datacenter
	switch *effective.Dc {
	case config_pb.DC_SINGLE:
		normalizedDatacenter = kratosconsul.SingleDatacenter
	case config_pb.DC_MULTI:
		normalizedDatacenter = kratosconsul.MultiDatacenter
	default:
		return discoveryConfig{}, fmt.Errorf(
			"consul discovery datacenter is unknown: %d",
			*effective.Dc,
		)
	}
	return discoveryConfig{
		timeout:    timeout,
		datacenter: normalizedDatacenter,
	}, nil
}
