package consul

import (
	"testing"
	"time"

	kratosconsul "github.com/go-kratos/kratos/contrib/registry/consul/v2"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// These cases kill mutations that omit defaults, accept non-positive durations,
// or map an unknown datacenter to a valid Consul option.
func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	defaults, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.timeout != 10*time.Second || defaults.datacenter != kratosconsul.SingleDatacenter {
		t.Fatalf("defaults = %#v", defaults)
	}

	dc := config_pb.DC_MULTI
	got, err := loadConfig(testconfig.New(t, "discovery", &config_pb.Discovery{Timeout: durationpb.New(3 * time.Second), Dc: &dc}))
	if err != nil {
		t.Fatal(err)
	}
	if got.timeout != 3*time.Second || got.datacenter != kratosconsul.MultiDatacenter {
		t.Fatalf("override = %#v", got)
	}
}

func TestLoadConfigRejectsInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		name  string
		value *config_pb.Discovery
	}{
		{name: "zero timeout", value: &config_pb.Discovery{Timeout: durationpb.New(0), Dc: config_pb.DC_SINGLE.Enum()}},
		{name: "unknown datacenter", value: &config_pb.Discovery{Timeout: durationpb.New(time.Second), Dc: func() *config_pb.DC { value := config_pb.DC(99); return &value }()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadConfig(testconfig.New(t, "discovery", test.value))
			if err == nil {
				t.Fatal("loadConfig error = nil")
			}
		})
	}
	if _, err := loadConfig(nil); err == nil || err.Error() != "consul discovery config manager is nil" {
		t.Fatalf("loadConfig(nil) error = %v, want contextual nil-manager error", err)
	}
}

func TestNewDiscoveryDisablesWithoutClient(t *testing.T) {
	logger := testLogger(t)
	discovery, err := NewDiscovery(logger, nil, nil)
	if err != nil || discovery != nil {
		t.Fatalf("NewDiscovery(nil client) = %v, %v", discovery, err)
	}
}

func TestNewDiscoveryBuildsAdapterForSharedClient(t *testing.T) {
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := NewDiscovery(testLogger(t), testconfig.Empty(t), client)
	if err != nil || discovery == nil {
		t.Fatalf("NewDiscovery() = %v, %v", discovery, err)
	}
}

func testLogger(t *testing.T) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := foundationlog.NewSharedState(foundationlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  foundationlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: foundationlog.FileConfig{OutputConfig: foundationlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return foundationlog.NewLogger(shared)
}
