package metadata

import (
	"fmt"
	"strings"

	metadata2 "github.com/go-kratos/kratos/v2/metadata"
	"github.com/go-kratos/kratos/v2/middleware"
	deadlinemiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/deadline"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/requestdebug"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

// Config 是元数据中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_Metadata

const (
	grpcTimeoutHeader = "grpc-timeout"
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	baggageHeader     = "baggage"
	serviceNameHeader = "x-md-service-name"
)

// Validate 校验元数据前缀，避免空前缀意外透传所有请求头。
func Validate(config Config) error {
	for index, prefix := range config.GetPrefix() {
		trimmed := strings.TrimSpace(prefix)
		if trimmed == "" {
			return fmt.Errorf("metadata prefix[%d] cannot be empty", index)
		}
		if trimmed != prefix {
			return fmt.Errorf("metadata prefix[%d] cannot contain surrounding whitespace: %q", index, prefix)
		}
	}
	return nil
}

// Server 创建服务端元数据透传中间件。
func Server(config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	opts := newMiddlewareOptions(config)
	return server(opts...)
}

// Client 创建客户端元数据透传中间件。
func Client(config Config) middleware.Middleware {
	if config.GetDisable() {
		return nil
	}

	opts := newMiddlewareOptions(config)
	return client(opts...)
}

// newMiddlewareOptions 将多份配置规整成稳定、无重复的中间件选项。
func newMiddlewareOptions(configs ...Config) []option {
	var opts []option
	// 前缀去重可以避免同一请求头被重复复制到业务元数据。
	seenPrefixes := make(map[string]struct{})
	var prefix []string
	for _, config := range configs {
		for _, value := range config.GetPrefix() {
			// HTTP 头名不区分大小写，统一小写后去重才能保证匹配结果稳定。
			value = strings.ToLower(value)
			if _, exists := seenPrefixes[value]; exists {
				continue
			}
			seenPrefixes[value] = struct{}{}
			prefix = append(prefix, value)
		}
	}
	if len(prefix) > 0 {
		opts = append(opts, withPropagatedPrefix(prefix...))
	}
	// 后置配置代表更具体的调用场景，因此同名常量应由后置配置覆盖。
	constantsList := make([]map[string]string, len(configs))
	for index, config := range configs {
		constantsList[index] = config.GetConstants()
	}
	constants := mergeConstantsMd(constantsList...)
	if len(constants) > 0 {
		opts = append(opts, withConstants(constants))
	}
	return opts
}

// mergeConstantsMd 合并常量元数据；后出现的配置覆盖同名键。
func mergeConstantsMd(constantsList ...map[string]string) metadata2.Metadata {
	md := metadata2.Metadata{}

	for _, constants := range constantsList {
		for k, v := range constants {
			if !isReservedMetadataKey(k) {
				md.Set(k, v)
			}
		}
	}

	return md
}

// option 配置元数据透传规则，仅供本包构造器使用。
type option func(*options)

type options struct {
	// prefix 保存允许透传的服务端元数据键前缀，默认 x-md-；显式客户端元数据不受前缀限制。
	prefix []string
	// md 保存每个请求携带的常量元数据，构造时剔除框架保留键，服务端使用时复制。
	md metadata2.Metadata
}

// hasPrefix 判断元数据键是否属于允许透传的前缀，并排除框架保留键。
// 空前缀会让 strings.HasPrefix 恒真，等价于透传全部请求头，因此即使调用方
// 绕过了 Validate 也必须在这里跳过。
func (o *options) hasPrefix(key string) bool {
	if isReservedMetadataKey(key) {
		return false
	}
	k := strings.ToLower(key)
	for _, prefix := range o.prefix {
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// isReservedMetadataKey 判断键是否由其他中间件拥有，避免职责重叠。
func isReservedMetadataKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case deadlinemiddleware.HTTPTimeoutHeader,
		grpcTimeoutHeader,
		traceparentHeader,
		tracestateHeader,
		baggageHeader,
		serviceNameHeader,
		requestdebug.Header:
		return true
	default:
		return false
	}
}

// withConstants 设置每个请求都携带的常量元数据。
func withConstants(md metadata2.Metadata) option {
	return func(o *options) {
		o.md = md
	}
}

// withPropagatedPrefix 设置允许跨服务透传的元数据前缀。
func withPropagatedPrefix(prefix ...string) option {
	return func(o *options) {
		o.prefix = prefix
	}
}
