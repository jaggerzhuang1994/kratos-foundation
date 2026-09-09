package tracing

import (
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

type configTestAppInfo struct {
	name string
}

func (configTestAppInfo) ID() string                  { return "test-id" }
func (i configTestAppInfo) Name() string              { return i.name }
func (configTestAppInfo) Version() string             { return "v1.0.0" }
func (configTestAppInfo) Metadata() map[string]string { return nil }

func TestLoadConfigAppliesTracingDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "prod")

	config, err := loadConfig(testconfig.Empty(t), configTestAppInfo{name: "orders"})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.GetDisable() {
		t.Fatal("disable = true, want false")
	}
	if got := config.GetExporter().GetEndpointUrl(); got != "http://localhost:4318/v1/traces" {
		t.Fatalf("exporter endpoint = %q", got)
	}
	if got := config.GetExporter().GetTimeout().AsDuration(); got != 10*time.Second {
		t.Fatalf("exporter timeout = %s, want 10s", got)
	}
	if got := config.GetSampler().GetSample(); got != config_pb.Sampler_RATIO {
		t.Fatalf("sampler policy = %v, want ratio", got)
	}
	if got := config.GetSampler().GetRatio(); got != 0.05 {
		t.Fatalf("sampler ratio = %v, want 0.05", got)
	}
}

func TestLoadConfigRejectsEmptyAppName(t *testing.T) {
	if _, err := loadConfig(testconfig.Empty(t), configTestAppInfo{}); err == nil {
		t.Fatal("loadConfig() error = nil, want empty app name error")
	}
}
