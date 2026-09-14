package tracing

import (
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"go.opentelemetry.io/otel"
	"google.golang.org/protobuf/proto"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

// samplerSnapshot 固化一次配置编译出的采样策略。
type samplerSnapshot struct {
	version     uint64
	config      *config_pb.Tracing
	sampler     tracesdk.Sampler
	description string
}

// dynamicSampler 让采样策略可以在运行期替换。
//
// OTel 的 TracerProvider 在构造后不能更换 Sampler，因此这里插入一层间接引用，把
// 采样决策推迟到每个 span 开始时读取，从而支持在不重启进程、不重建 provider 和
// exporter 的情况下调整采样率。
type dynamicSampler struct {
	config  *foundationconfig.HotReloadValue[config_pb.Tracing]
	current atomic.Pointer[samplerSnapshot]
}

var _ tracesdk.Sampler = (*dynamicSampler)(nil)

// newDynamicSampler 编译当前配置；初始配置非法时由组装层取消订阅。
func newDynamicSampler(config *foundationconfig.HotReloadValue[config_pb.Tracing]) (*dynamicSampler, error) {
	current, version := config.GetCurrent()
	if err := current.ValidateAll(); err != nil {
		return nil, err
	}
	compiled, err := newSampler(current)
	if err != nil {
		return nil, err
	}
	sampler := &dynamicSampler{config: config}
	sampler.current.Store(&samplerSnapshot{version: version, config: current, sampler: compiled, description: compiled.Description()})
	return sampler, nil
}

// ShouldSample 按当前生效的策略决定是否记录该 span。
func (s *dynamicSampler) ShouldSample(parameters tracesdk.SamplingParameters) tracesdk.SamplingResult {
	return s.snapshot().sampler.ShouldSample(parameters)
}

// Description 暴露当前内部策略，便于排查线上实际生效的采样配置。
func (s *dynamicSampler) Description() string {
	return "DynamicSampler{" + s.snapshot().description + "}"
}

// snapshot 仅在配置版本变化时编译；并发发布不得使缓存退回旧版本。
func (s *dynamicSampler) snapshot() *samplerSnapshot {
	for {
		old := s.current.Load()
		current, version := s.config.GetCurrent()
		if old.version >= version {
			return old
		}
		next := *old
		next.version = version
		err := current.ValidateAll()
		var compiled tracesdk.Sampler
		if err == nil {
			compiled, err = newSampler(current)
		}
		if err == nil {
			next.config = current
			next.sampler = compiled
			next.description = compiled.Description()
		}
		// 失败版本也登记为已处理，保留有效策略，避免每个 span 重试和重复报错。
		if !s.current.CompareAndSwap(old, &next) {
			continue
		}
		if err != nil {
			otel.Handle(fmt.Errorf("dynamicSampler.snapshot rejected tracing sampler configuration update at version %d: %w", version, err))
		} else if !samplerOnlyChange(old.config, current) {
			otel.Handle(errors.New("dynamicSampler.snapshot only applies sampler hot updates; restart the application to change exporter or disable settings"))
		}
		return &next
	}
}

// samplerOnlyChange 比较非采样字段；这些资源配置只有重启 Provider 才能生效。
func samplerOnlyChange(current, next *config_pb.Tracing) bool {
	currentRest := proto.CloneOf(current)
	nextRest := proto.CloneOf(next)
	currentRest.Sampler = nil
	nextRest.Sampler = nil
	return proto.Equal(currentRest, nextRest)
}

// newSampler 将配置枚举转换为 OpenTelemetry 采样策略并拒绝无效比例。
func newSampler(config *config_pb.Tracing) (tracesdk.Sampler, error) {
	samplerConfig := config.GetSampler()
	if samplerConfig == nil || samplerConfig.Sample == nil {
		return nil, errors.New("tracing sampler is required")
	}
	switch samplerConfig.GetSample() {
	case config_pb.Sampler_RATIO:
		if samplerConfig.Ratio == nil {
			return nil, errors.New("tracing sampler ratio is required")
		}
		ratio := samplerConfig.GetRatio()
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return nil, fmt.Errorf("tracing sampler ratio must be between 0 and 1")
		}
		return tracesdk.ParentBased(tracesdk.TraceIDRatioBased(ratio)), nil
	case config_pb.Sampler_ALWAYS:
		return tracesdk.ParentBased(tracesdk.AlwaysSample()), nil
	case config_pb.Sampler_NEVER:
		return tracesdk.ParentBased(tracesdk.NeverSample()), nil
	default:
		return nil, fmt.Errorf(
			"unsupported tracing sampler %q",
			samplerConfig.GetSample().String(),
		)
	}
}
