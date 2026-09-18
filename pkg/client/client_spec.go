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
	middleware *clientMiddlewareConfig
}

// clientMiddlewareConfig 是具名客户端直接策略字段的规范化快照，只用于内部比较和构建，
// 不形成额外的配置路径。
type clientMiddlewareConfig struct {
	metadata       *config_pb.Middleware_Metadata
	tracing        *config_pb.Middleware_Tracing
	metrics        *config_pb.Middleware_Metrics
	logging        *config_pb.Middleware_Logging
	circuitBreaker *config_pb.Middleware_CircuitBreaker
	deadline       *config_pb.Middleware_Deadline
	requestDebug   *config_pb.Middleware_RequestDebug
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

	middleware := &clientMiddlewareConfig{
		metadata:       proto.CloneOf(option.GetMetadata()),
		tracing:        proto.CloneOf(option.GetTracing()),
		metrics:        proto.CloneOf(option.GetMetrics()),
		logging:        proto.CloneOf(option.GetLogging()),
		circuitBreaker: proto.CloneOf(option.GetCircuitBreaker()),
		deadline:       proto.CloneOf(option.GetDeadline()),
		requestDebug:   proto.CloneOf(option.GetRequestDebug()),
	}
	// 在独立副本上逐字段继承，必须先于零值规范化，保证显式 0s 可以覆盖根配置。
	if middleware.deadline == nil {
		middleware.deadline = new(config_pb.Middleware_Deadline)
	}
	d := middleware.deadline
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

func canonicalizeClientMiddleware(middleware *clientMiddlewareConfig) {
	if deadlineConfig := middleware.deadline; deadlineConfig != nil {
		// fallback_timeout 缺失时默认 10s，显式 0s 必须保留以关闭回退超时。
		zeroDuration := new(durationpb.Duration)
		if proto.Equal(deadlineConfig.MaxTimeout, zeroDuration) {
			deadlineConfig.MaxTimeout = nil
		}
		if proto.Equal(deadlineConfig.MinBudget, zeroDuration) {
			deadlineConfig.MinBudget = nil
		}
		if proto.Equal(deadlineConfig, new(config_pb.Middleware_Deadline)) {
			middleware.deadline = nil
		}
	}
	if metadataConfig := middleware.metadata; metadataConfig != nil {
		if metadataConfig.Disable != nil && !metadataConfig.GetDisable() {
			metadataConfig.Disable = nil
		}
		if proto.Equal(metadataConfig, new(config_pb.Middleware_Metadata)) {
			middleware.metadata = nil
		}
	}
	if tracingConfig := middleware.tracing; tracingConfig != nil {
		if tracingConfig.Disable != nil && !tracingConfig.GetDisable() {
			tracingConfig.Disable = nil
		}
		if proto.Equal(tracingConfig, new(config_pb.Middleware_Tracing)) {
			middleware.tracing = nil
		}
	}
	if metricsConfig := middleware.metrics; metricsConfig != nil {
		if metricsConfig.Disable != nil && !metricsConfig.GetDisable() {
			metricsConfig.Disable = nil
		}
		if proto.Equal(metricsConfig, new(config_pb.Middleware_Metrics)) {
			middleware.metrics = nil
		}
	}
	if loggingConfig := middleware.logging; loggingConfig != nil {
		if loggingConfig.Disable != nil && !loggingConfig.GetDisable() {
			loggingConfig.Disable = nil
		}
		if proto.Equal(loggingConfig, new(config_pb.Middleware_Logging)) {
			middleware.logging = nil
		}
	}
	if circuitBreakerConfig := middleware.circuitBreaker; circuitBreakerConfig != nil {
		if !circuitBreakerConfig.GetEnable() {
			middleware.circuitBreaker = nil
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
		s.middleware.equal(other.middleware)
}

func (c *clientMiddlewareConfig) equal(other *clientMiddlewareConfig) bool {
	return proto.Equal(c.metadata, other.metadata) &&
		proto.Equal(c.tracing, other.tracing) &&
		proto.Equal(c.metrics, other.metrics) &&
		proto.Equal(c.logging, other.logging) &&
		proto.Equal(c.circuitBreaker, other.circuitBreaker) &&
		proto.Equal(c.deadline, other.deadline) &&
		proto.Equal(c.requestDebug, other.requestDebug)
}

func (c *clientMiddlewareConfig) GetMetadata() *config_pb.Middleware_Metadata { return c.metadata }
func (c *clientMiddlewareConfig) GetTracing() *config_pb.Middleware_Tracing   { return c.tracing }
func (c *clientMiddlewareConfig) GetMetrics() *config_pb.Middleware_Metrics   { return c.metrics }
func (c *clientMiddlewareConfig) GetLogging() *config_pb.Middleware_Logging   { return c.logging }
func (c *clientMiddlewareConfig) GetCircuitBreaker() *config_pb.Middleware_CircuitBreaker {
	return c.circuitBreaker
}
func (c *clientMiddlewareConfig) GetDeadline() *config_pb.Middleware_Deadline {
	return c.deadline
}
func (c *clientMiddlewareConfig) GetRequestDebug() *config_pb.Middleware_RequestDebug {
	return c.requestDebug
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
