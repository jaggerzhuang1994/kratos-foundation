package client

import (
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	metadatamiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metadata"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client/internal/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type clientSpec struct {
	// name 客户端连接名称。
	name string
	// discovery 已解析的服务发现实例名；依次继承连接、根配置，最终默认 default。
	discovery string
	// protocol 客户端传输协议。
	protocol config_pb.Protocol
	// target 连接目标；省略时使用 discovery:/// 加连接名。
	target string
	// middleware 已复制并归一化的中间件配置，作为版本比较依据。
	middleware *config_pb.ClientMiddleware
}

func newClientSpec(name string, option *config_pb.ClientOption, defaults *config_pb.Client) clientSpec {
	target := option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", name)
	}

	discovery := option.GetDiscovery()
	if discovery == "" {
		discovery = defaults.GetDiscovery()
	}
	if discovery == "" {
		discovery = "default"
	}

	middleware := option.GetMiddleware()
	if middleware == nil {
		middleware = new(config_pb.ClientMiddleware)
	} else {
		middleware = proto.Clone(middleware).(*config_pb.ClientMiddleware)
	}
	// 在独立副本上逐字段继承，必须先于零值规范化，保证显式 0s 可以覆盖根配置。
	if middleware.Deadline == nil {
		middleware.Deadline = new(config_pb.Middleware_Deadline)
	}
	d := middleware.Deadline
	if d.FallbackTimeout == nil {
		d.FallbackTimeout = proto.CloneOf(defaults.GetFallbackTimeout())
	}
	if d.MaxTimeout == nil {
		d.MaxTimeout = proto.CloneOf(defaults.GetMaxTimeout())
	}
	if d.MinBudget == nil {
		d.MinBudget = proto.CloneOf(defaults.GetMinBudget())
	}
	canonicalizeClientMiddleware(middleware)

	return clientSpec{
		name:       name,
		discovery:  discovery,
		protocol:   option.GetProtocol(),
		target:     target,
		middleware: middleware,
	}
}

func canonicalizeClientMiddleware(middleware *config_pb.ClientMiddleware) {
	if deadlineConfig := middleware.Deadline; deadlineConfig != nil {
		// fallback_timeout 缺失时默认 10s，显式 0s 必须保留以关闭回退超时。
		zeroDuration := new(durationpb.Duration)
		if proto.Equal(deadlineConfig.MaxTimeout, zeroDuration) {
			deadlineConfig.MaxTimeout = nil
		}
		if proto.Equal(deadlineConfig.MinBudget, zeroDuration) {
			deadlineConfig.MinBudget = nil
		}
		if proto.Equal(deadlineConfig, new(config_pb.Middleware_Deadline)) {
			middleware.Deadline = nil
		}
	}
	if metadataConfig := middleware.Metadata; metadataConfig != nil {
		if metadataConfig.Disable != nil && !metadataConfig.GetDisable() {
			metadataConfig.Disable = nil
		}
		if proto.Equal(metadataConfig, new(config_pb.Middleware_Metadata)) {
			middleware.Metadata = nil
		}
	}
	if tracingConfig := middleware.Tracing; tracingConfig != nil {
		if tracingConfig.Disable != nil && !tracingConfig.GetDisable() {
			tracingConfig.Disable = nil
		}
		if proto.Equal(tracingConfig, new(config_pb.Middleware_Tracing)) {
			middleware.Tracing = nil
		}
	}
	if metricsConfig := middleware.Metrics; metricsConfig != nil {
		if metricsConfig.Disable != nil && !metricsConfig.GetDisable() {
			metricsConfig.Disable = nil
		}
		if proto.Equal(metricsConfig, new(config_pb.Middleware_Metrics)) {
			middleware.Metrics = nil
		}
	}
	if loggingConfig := middleware.Logging; loggingConfig != nil {
		if loggingConfig.Disable != nil && !loggingConfig.GetDisable() {
			loggingConfig.Disable = nil
		}
		if proto.Equal(loggingConfig, new(config_pb.Middleware_Logging)) {
			middleware.Logging = nil
		}
	}
	if circuitBreakerConfig := middleware.CircuitBreaker; circuitBreakerConfig != nil {
		if !circuitBreakerConfig.GetEnable() {
			middleware.CircuitBreaker = nil
		} else if proto.Equal(
			circuitBreakerConfig.Sre,
			new(config_pb.Middleware_CircuitBreaker_SREBreaker),
		) {
			circuitBreakerConfig.Sre = nil
		}
	}
}

func (s clientSpec) equal(other clientSpec) bool {
	return s.protocol == other.protocol &&
		s.target == other.target &&
		s.discovery == other.discovery &&
		proto.Equal(s.middleware, other.middleware)
}

func (s clientSpec) useDiscovery() bool {
	return strings.HasPrefix(s.target, "discovery://")
}

func (b *builder) validateConfig(config *config_pb.Client) error {
	if _, err := clientCleanupTimeout(config); err != nil {
		return err
	}
	if err := config.ValidateAll(); err != nil {
		return fmt.Errorf("validate client config: %w", err)
	}
	// 即使没有具名客户端，也校验按名称动态获取时会使用的根策略。
	rootSpec := newClientSpec("", nil, config)
	if _, err := deadline.NewStore(rootSpec.middleware.GetDeadline()); err != nil {
		return fmt.Errorf("client default deadline: %w", err)
	}
	for name, option := range config.GetClients() {
		spec := newClientSpec(name, option, config)
		if spec.useDiscovery() && (b.discoveries != nil || option.GetDiscovery() != "" || config.GetDiscovery() != "") {
			if _, err := b.resolveDiscovery(spec); err != nil {
				return err
			}
		}
		protocol := option.GetProtocol()
		switch protocol {
		case config_pb.Protocol_GRPC, config_pb.Protocol_HTTP, config_pb.Protocol_HTTPS:
		default:
			return fmt.Errorf("client %q: %w: %s (%d)", name, ErrInvalidProtocol, protocol, protocol)
		}
		middleware := spec.middleware
		if _, err := deadline.NewStore(middleware.GetDeadline()); err != nil {
			return fmt.Errorf("client %q deadline: %w", name, err)
		}
		if err := metadatamiddleware.Validate(middleware.GetMetadata()); err != nil {
			return fmt.Errorf("client %q metadata: %w", name, err)
		}
		if err := circuitbreaker.Validate(middleware.GetCircuitBreaker()); err != nil {
			return fmt.Errorf("client %q circuit breaker: %w", name, err)
		}
	}
	return nil
}
