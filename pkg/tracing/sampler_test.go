package tracing

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

func TestDynamicSamplerHonorsParentDecisionForEveryPolicy(t *testing.T) {
	sampledParent := parentContext(trace.FlagsSampled)
	unsampledParent := parentContext(0)

	tests := []struct {
		name     string
		config   *config_pb.Tracing
		parent   context.Context
		decision tracesdk.SamplingDecision
	}{
		{
			name: "always samples a root trace",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_ALWAYS.Enum(),
			}},
			parent:   context.Background(),
			decision: tracesdk.RecordAndSample,
		},
		{
			name: "always preserves an unsampled parent",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_ALWAYS.Enum(),
			}},
			parent:   unsampledParent,
			decision: tracesdk.Drop,
		},
		{
			name: "never drops a root trace",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_NEVER.Enum(),
			}},
			parent:   context.Background(),
			decision: tracesdk.Drop,
		},
		{
			name: "never preserves a sampled parent",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_NEVER.Enum(),
			}},
			parent:   sampledParent,
			decision: tracesdk.RecordAndSample,
		},
		{
			name: "zero ratio drops a root trace",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_RATIO.Enum(),
				Ratio:  proto.Float64(0),
			}},
			parent:   context.Background(),
			decision: tracesdk.Drop,
		},
		{
			name: "zero ratio preserves a sampled parent",
			config: &config_pb.Tracing{Sampler: &config_pb.Sampler{
				Sample: config_pb.Sampler_RATIO.Enum(),
				Ratio:  proto.Float64(0),
			}},
			parent:   sampledParent,
			decision: tracesdk.RecordAndSample,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sampler, _, err := newTestDynamicSampler(t, test.config)
			if err != nil {
				t.Fatalf("newDynamicSampler() error = %v", err)
			}
			result := sampler.ShouldSample(tracesdk.SamplingParameters{
				ParentContext: test.parent,
				TraceID:       trace.TraceID{1},
				Name:          "test.operation",
			})
			if result.Decision != test.decision {
				t.Fatalf("decision = %v, want %v", result.Decision, test.decision)
			}
		})
	}
}

func TestDynamicSamplerRejectsInvalidUpdatesWithoutReplacingActivePolicy(t *testing.T) {
	sampler, manager, err := newTestDynamicSampler(t, &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_ALWAYS.Enum()}})
	if err != nil {
		t.Fatal(err)
	}
	manager.observer("tracing", &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_RATIO.Enum(), Ratio: proto.Float64(math.NaN())}}, nil)

	if got := sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}}).Decision; got != tracesdk.RecordAndSample {
		t.Fatalf("invalid update replaced active sampler: %v", got)
	}
	if _, _, err := newTestDynamicSampler(t, &config_pb.Tracing{}); err == nil {
		t.Fatal("missing sampler accepted")
	}
}

func TestDynamicSamplerConcurrentUpdatesAndReads(t *testing.T) {
	sampler, manager, err := newTestDynamicSampler(t, &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_NEVER.Enum()}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.observer("tracing", &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_ALWAYS.Enum()}}, nil)
			_ = sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}})
			_ = sampler.Description()
		}()
	}
	wg.Wait()
}

func TestDynamicSamplerUpdateReplacesTheActivePolicy(t *testing.T) {
	sampler, manager, err := newTestDynamicSampler(t, &config_pb.Tracing{Sampler: &config_pb.Sampler{
		Sample: config_pb.Sampler_NEVER.Enum(),
	}})
	if err != nil {
		t.Fatalf("newDynamicSampler() error = %v", err)
	}
	manager.observer("tracing", &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_ALWAYS.Enum()}}, nil)

	result := sampler.ShouldSample(tracesdk.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{1},
		Name:          "test.operation",
	})
	if result.Decision != tracesdk.RecordAndSample {
		t.Fatalf("decision after update = %v, want %v", result.Decision, tracesdk.RecordAndSample)
	}
}

func parentContext(flags trace.TraceFlags) context.Context {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: flags,
		Remote:     true,
	})
	return trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
}

func newTestDynamicSampler(t *testing.T, initial *config_pb.Tracing) (*dynamicSampler, *tracingManagerStub, error) {
	t.Helper()
	manager := &tracingManagerStub{initial: initial}
	hot, cancel, err := foundationconfig.NewHotReloadValue[config_pb.Tracing](manager, "tracing", initial)
	if err != nil {
		return nil, manager, err
	}
	t.Cleanup(cancel)
	sampler, err := newDynamicSampler(hot)
	return sampler, manager, err
}

func TestDynamicSamplerReportsRejectedVersionOnceAndRecovers(t *testing.T) {
	previous := otel.GetErrorHandler()
	var reports []error
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { reports = append(reports, err) }))
	t.Cleanup(func() { otel.SetErrorHandler(previous) })
	sampler, manager, err := newTestDynamicSampler(t, tracingSamplerConfig(config_pb.Sampler_ALWAYS))
	if err != nil {
		t.Fatal(err)
	}
	// 非采样配置变更仍应用采样策略，并提示资源变更需要重启。
	changed := tracingSamplerConfig(config_pb.Sampler_NEVER)
	changed.Disable = proto.Bool(true)
	manager.observer("tracing", changed, nil)
	if got := sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}}).Decision; got != tracesdk.Drop {
		t.Fatalf("decision = %v", got)
	}
	if len(reports) != 1 || !strings.Contains(reports[0].Error(), "restart") {
		t.Fatalf("reports = %v", reports)
	}
	manager.observer("tracing", &config_pb.Tracing{Sampler: &config_pb.Sampler{Sample: config_pb.Sampler_RATIO.Enum(), Ratio: proto.Float64(math.NaN())}}, nil)
	for range 3 {
		sampler.Description()
	}
	if len(reports) != 2 {
		t.Fatalf("invalid version reports = %v", reports)
	}
	if got := sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}}).Decision; got != tracesdk.Drop {
		t.Fatalf("invalid config changed decision = %v", got)
	}
	valid := proto.CloneOf(changed)
	valid.Sampler.Sample = config_pb.Sampler_ALWAYS.Enum()
	manager.observer("tracing", valid, nil)
	if got := sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}}).Decision; got != tracesdk.RecordAndSample {
		t.Fatalf("recovery decision = %v", got)
	}
	// 订阅解码失败由 HotReloadValue 保留上次快照。
	manager.observer("tracing", nil, errors.New("decode failed"))
	if got := sampler.ShouldSample(tracesdk.SamplingParameters{TraceID: trace.TraceID{1}}).Decision; got != tracesdk.RecordAndSample {
		t.Fatalf("decode error changed decision = %v", got)
	}
}

func TestSamplerOnlyChange(t *testing.T) {
	initial := tracingSamplerConfig(config_pb.Sampler_NEVER)
	onlySampler := tracingSamplerConfig(config_pb.Sampler_ALWAYS)
	if !samplerOnlyChange(initial, onlySampler) {
		t.Fatal("samplerOnlyChange() = false for sampler-only update")
	}

	disableChanged := tracingSamplerConfig(config_pb.Sampler_ALWAYS)
	disableChanged.Disable = boolPointer(true)
	if samplerOnlyChange(initial, disableChanged) {
		t.Fatal("samplerOnlyChange() = true after disable changed")
	}
}
