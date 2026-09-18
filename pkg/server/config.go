package server

import (
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/deadline"
	metadatamiddleware "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/middleware/metadata"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server/internal/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type componentConfig = *config_pb.Server

// defaultConfig 只作为合并模板使用，Manager.Load 会先复制它。
var defaultConfig = &config_pb.Server{
	StopDelay: durationpb.New(0),
	Http: &config_pb.HttpServerOption{
		Disable:            proto.Bool(false),
		Network:            proto.String("tcp"),
		Addr:               proto.String("0.0.0.0:8000"),
		Endpoint:           nil,
		DisableStrictSlash: proto.Bool(false),
		PathPrefix:         proto.String(""),
		Health: &config_pb.HttpServerOption_Health{
			Disable:       proto.Bool(false),
			LivenessPath:  proto.String("/healthz"),
			ReadinessPath: proto.String("/readyz"),
			Timeout:       durationpb.New(time.Second),
		},
		Metrics: &config_pb.HttpServerOption_Metrics{
			Disable: proto.Bool(false),
			Path:    proto.String("/metrics"),
		},
	},
	Grpc: &config_pb.GrpcServerOption{
		// 保留省略状态，才能区分按服务注册启用与配置显式开启。
		Disable:           nil,
		Network:           proto.String("tcp"),
		Addr:              proto.String("0.0.0.0:9000"),
		Endpoint:          nil,
		CustomHealth:      proto.Bool(false),
		DisableReflection: proto.Bool(false),
	},
}

// loadConfig 合并默认值并校验服务端配置，确保构造阶段得到完整快照。
func loadConfig(manager foundationconfig.Manager) (componentConfig, error) {
	next := new(config_pb.Server)
	if err := manager.Load("server", next, defaultConfig); err != nil {
		return nil, err
	}
	if err := validateConfig(next); err != nil {
		return nil, err
	}
	return next, nil
}

// validateConfig 统一检查生成约束、停机时间和中间件策略。
func validateConfig(config componentConfig) error {
	if err := config.ValidateAll(); err != nil {
		return fmt.Errorf("validate server config: %w", err)
	}
	if stopDelay := config.GetStopDelay().AsDuration(); stopDelay < 0 {
		return fmt.Errorf("server stop_delay cannot be negative: %s", stopDelay)
	}
	return validateMiddlewareConfig(config)
}

// validateMiddlewareConfig 校验可热更新的中间件策略。
//
// 热更新订阅完整 server 快照，但只应用这些策略；启动期的 validateConfig 也复用它，
// 避免两条路径出现不同的接受标准。
func validateMiddlewareConfig(config *config_pb.Server) error {
	if _, err := deadline.NewStore(config.GetDeadline()); err != nil {
		return fmt.Errorf("server deadline: %w", err)
	}
	if err := metadatamiddleware.Validate(config.GetMetadata()); err != nil {
		return fmt.Errorf("server metadata: %w", err)
	}
	if err := ratelimit.Validate(config.GetRateLimit()); err != nil {
		return fmt.Errorf("server rate limit: %w", err)
	}
	return nil
}

// configuredHealth 读取部署配置并复制代码声明的检查函数；代码不能覆盖端点开关。
func configuredHealth(config componentConfig, spec *Spec) *healthState {
	source := config.GetHttp().GetHealth()
	next := healthConfig{
		Disable:       source.GetDisable(),
		Addr:          source.GetAddr(),
		LivenessPath:  source.GetLivenessPath(),
		ReadinessPath: source.GetReadinessPath(),
		Timeout:       source.GetTimeout().AsDuration(),
		Checks:        spec.health.checks,
	}
	return newHealthState(next)
}
