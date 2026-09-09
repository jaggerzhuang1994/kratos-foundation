package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNewConfigPreservesManagerLoadFailure(t *testing.T) {
	cause := errors.New("load failed")
	_, err := NewConfig(&stopPolicyConfigManager{loadErr: cause})
	if !errors.Is(err, cause) {
		t.Fatalf("NewConfig load error = %v", err)
	}
}

func TestValidateConfigRejectsMalformedAndNonPositiveTimeouts(t *testing.T) {
	malformed := validAppConfig(time.Second)
	malformed.RegistrarTimeout = &durationpb.Duration{Nanos: 1_000_000_000}
	if err := validateConfig(malformed); err == nil || !strings.Contains(err.Error(), "registrar_timeout is invalid") {
		t.Fatalf("malformed protobuf duration error = %v", err)
	}
	malformedStop := validAppConfig(time.Second)
	malformedStop.StopTimeout = &durationpb.Duration{Nanos: 1_000_000_000}
	if err := validateConfig(malformedStop); err == nil || !strings.Contains(err.Error(), "stop_timeout is invalid") {
		t.Fatalf("malformed stop duration error = %v", err)
	}

	invalidRegistrar := &config_pb.App{
		RegistrarTimeout: durationpb.New(0),
		StopTimeout:      durationpb.New(time.Second),
	}
	if err := validateConfig(invalidRegistrar); err == nil ||
		!strings.Contains(err.Error(), "registrar_timeout must be positive") {
		t.Fatalf("zero registrar timeout error = %v", err)
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	config, err := NewConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	if config.GetRegistrarTimeout().AsDuration() != 10*time.Second || config.GetStopTimeout().AsDuration() != 30*time.Second {
		t.Fatalf("defaults = %#v", config)
	}
	_, err = NewConfig(testconfig.New(t, "app", &config_pb.App{StopTimeout: durationpb.New(0)}))
	if err == nil {
		t.Fatal("zero stop timeout accepted")
	}
	if err := validateStopTimeout(time.Second, time.Second); err == nil {
		t.Fatal("stop timeout equal delay accepted")
	}
}
