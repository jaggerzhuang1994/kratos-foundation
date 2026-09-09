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
	name       string
	protocol   config_pb.Protocol
	target     string
	middleware *config_pb.ClientMiddleware
}

func newClientSpec(name string, option *config_pb.ClientOption) clientSpec {
	target := option.GetTarget()
	if target == "" {
		target = fmt.Sprintf("discovery:///%s", name)
	}

	middleware := option.GetMiddleware()
	if middleware == nil {
		middleware = new(config_pb.ClientMiddleware)
	} else {
		middleware = proto.Clone(middleware).(*config_pb.ClientMiddleware)
	}
	canonicalizeClientMiddleware(middleware)

	return clientSpec{
		name:       name,
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
	for name, option := range config.GetClients() {
		if option == nil {
			continue
		}
		protocol := option.GetProtocol()
		switch protocol {
		case config_pb.Protocol_GRPC, config_pb.Protocol_HTTP, config_pb.Protocol_HTTPS:
		default:
			return fmt.Errorf("client %q: %w: %s (%d)", name, ErrInvalidProtocol, protocol, protocol)
		}
		middleware := option.GetMiddleware()
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
