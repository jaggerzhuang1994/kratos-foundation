package consul

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// These cases kill mutations that truncate fractional intervals, accept duplicate
// tags, or return the caller's tag backing array directly.
func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	defaults, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.healthCheckIntervalSeconds != 10 || defaults.deregisterCriticalAfterSeconds != 600 || defaults.disableHealthCheck || defaults.disableHeartbeat {
		t.Fatalf("defaults = %#v", defaults)
	}
	value := &config_pb.Registry{DisableHealthCheck: proto.Bool(true), DisableHeartbeat: proto.Bool(true), HealthcheckInternal: durationpb.New(2 * time.Second), DeregisterCriticalServiceAfter: durationpb.New(5 * time.Second), Tags: []string{"blue", "api"}}
	got, err := loadConfig(testconfig.New(t, "registry", value))
	if err != nil {
		t.Fatal(err)
	}
	if !got.disableHealthCheck || !got.disableHeartbeat || got.healthCheckIntervalSeconds != 2 || got.deregisterCriticalAfterSeconds != 5 {
		t.Fatalf("override = %#v", got)
	}
	if len(got.tags) != 2 || got.tags[0] != "blue" {
		t.Fatalf("tags = %#v", got.tags)
	}
}

func TestIntervalSecondsAndTagsRejectInvalidBoundaries(t *testing.T) {
	for _, interval := range []*durationpb.Duration{nil, durationpb.New(999 * time.Millisecond), durationpb.New(1500 * time.Millisecond)} {
		if _, err := intervalSeconds("interval", interval); err == nil {
			t.Fatal("intervalSeconds error = nil")
		}
	}
	if got, err := intervalSeconds("interval", durationpb.New(2*time.Second)); err != nil || got != 2 {
		t.Fatalf("intervalSeconds = %d, %v", got, err)
	}
	for _, tags := range [][]string{{""}, {" api"}, {"api", "api"}} {
		if _, err := validateTags(tags); err == nil {
			t.Fatalf("validateTags(%#v) error = nil", tags)
		}
	}
	input := []string{"api"}
	tags, err := validateTags(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = "changed"
	if tags[0] != "api" {
		t.Fatalf("validated tag changed with input: %#v", tags)
	}
}

func TestNewRegistryDisablesWithoutClient(t *testing.T) {
	shared, cleanup, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	registrar, err := NewRegistry(shared, nil, nil)
	if err != nil || registrar != nil {
		t.Fatalf("NewRegistry(nil client) = %v, %v", registrar, err)
	}
}

func TestNewRegistryBuildsAdapterForSharedClient(t *testing.T) {
	shared, cleanup, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std:  testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	registrar, err := NewRegistry(shared, testconfig.Empty(t), client)
	if err != nil || registrar == nil {
		t.Fatalf("NewRegistry() = %v, %v", registrar, err)
	}
}
