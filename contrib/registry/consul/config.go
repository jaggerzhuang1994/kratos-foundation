package consul

import (
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type registryConfig struct {
	disableHealthCheck             bool
	disableHeartbeat               bool
	healthCheckIntervalSeconds     int
	deregisterCriticalAfterSeconds int
	tags                           []string
}

// loadConfig 一次完成注册配置的默认值合并、protobuf 校验和运行时归一化。
func loadConfig(config config.Reader) (registryConfig, error) {
	defaults := &config_pb.RegistrarOptions{
		DisableHealthCheck:             proto.Bool(false),
		DisableHeartbeat:               proto.Bool(false),
		HealthcheckInternal:            durationpb.New(10 * time.Second),
		DeregisterCriticalServiceAfter: durationpb.New(10 * time.Minute),
	}
	effective := new(config_pb.RegistrarOptions)
	if err := config.Load("registry", effective, defaults); err != nil {
		return registryConfig{}, fmt.Errorf("load Consul registry config: %w", err)
	}
	// HealthcheckInternal 是历史 protobuf 字段名，实际语义是 health-check interval。
	// 在边界立即转成秒数，避免这个易混名称继续扩散。
	healthCheckSeconds, err := intervalSeconds(
		"consul registry health-check interval",
		effective.HealthcheckInternal,
	)
	if err != nil {
		return registryConfig{}, err
	}
	deregisterSeconds, err := intervalSeconds(
		"consul registry deregistration interval",
		effective.DeregisterCriticalServiceAfter,
	)
	if err != nil {
		return registryConfig{}, err
	}
	tags, err := validateTags(effective.GetTags())
	if err != nil {
		return registryConfig{}, err
	}
	return registryConfig{
		disableHealthCheck:             effective.GetDisableHealthCheck(),
		disableHeartbeat:               effective.GetDisableHeartbeat(),
		healthCheckIntervalSeconds:     healthCheckSeconds,
		deregisterCriticalAfterSeconds: deregisterSeconds,
		tags:                           tags,
	}, nil
}

// intervalSeconds 要求 Consul 间隔是至少一秒的整秒，避免下游 int 秒选项静默截断精度。
func intervalSeconds(name string, interval *durationpb.Duration) (int, error) {
	if interval == nil {
		return 0, fmt.Errorf("%s is required", name)
	}
	if err := interval.CheckValid(); err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	duration := interval.AsDuration()
	if duration < time.Second {
		return 0, fmt.Errorf("%s must be at least one second", name)
	}
	if duration%time.Second != 0 {
		return 0, fmt.Errorf("%s must use whole seconds", name)
	}
	return int(duration / time.Second), nil
}

// validateTags 拒绝空白、首尾空白或重复标签，并返回独立切片防止配置快照被下游修改。
func validateTags(tags []string) ([]string, error) {
	validated := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			return nil, errors.New("consul registry tag is empty")
		}
		if trimmed != tag {
			return nil, fmt.Errorf(
				"consul registry tag has surrounding whitespace: %q",
				tag,
			)
		}
		if _, exists := seen[tag]; exists {
			return nil, fmt.Errorf("consul registry tag is duplicated: %q", tag)
		}
		seen[tag] = struct{}{}
		validated = append(validated, tag)
	}
	return validated, nil
}
