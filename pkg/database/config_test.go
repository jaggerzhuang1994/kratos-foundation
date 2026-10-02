package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func validationStubDriver(DriverConfig) (DriverConnection, error) {
	return DriverConnection{}, nil
}

type loadErrorDatabaseConfigManager struct {
	foundationconfig.Manager
	key string
	err error
}

func (m loadErrorDatabaseConfigManager) Load(key string, target any, defaults ...any) error {
	if key == m.key {
		return m.err
	}
	return m.Manager.Load(key, target, defaults...)
}

func (loadErrorDatabaseConfigManager) Subscribe(
	string,
	any,
	foundationconfig.Observer,
	...any,
) (func(), error) {
	return nil, errors.New("Subscribe must not be called by loadConfig")
}

func TestLoadConfigInheritsTracingDefaultsAndPreservesOverrides(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		global   *config_pb.Tracing
		local    *config_pb.GormTracing
		disabled bool
	}{
		{name: "local environment default", env: "local", disabled: true},
		{name: "production environment default", env: "prod"},
		{name: "global disabled", env: "prod", global: &config_pb.Tracing{Disable: proto.Bool(true)}, disabled: true},
		{name: "global enabled", env: "local", global: &config_pb.Tracing{Disable: proto.Bool(false)}},
		{name: "local disabled override", env: "prod", global: &config_pb.Tracing{Disable: proto.Bool(false)}, local: &config_pb.GormTracing{Disable: proto.Bool(true)}, disabled: true},
		{name: "local enabled override", env: "prod", global: &config_pb.Tracing{Disable: proto.Bool(true)}, local: &config_pb.GormTracing{Disable: proto.Bool(false)}},
		{name: "empty local message", env: "prod", global: &config_pb.Tracing{Disable: proto.Bool(true)}, local: &config_pb.GormTracing{}, disabled: true},
		{name: "local fields without disable", env: "prod", global: &config_pb.Tracing{Disable: proto.Bool(true)}, local: &config_pb.GormTracing{ExcludeQueryVars: proto.Bool(true)}, disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.env)
			component := &config_pb.Database{
				Connections: map[string]*config_pb.DBConnection{
					"default": {Driver: proto.String("mysql"), Dsn: "fixture-dsn"},
				},
				Tracing: tt.local,
			}
			sections := map[string]proto.Message{"database": component}
			if tt.global != nil {
				sections["tracing"] = tt.global
			}
			loaded, err := loadConfig(testconfig.NewMany(t, sections), map[string]DriverFactory{"mysql": validationStubDriver})
			if err != nil {
				t.Fatal(err)
			}
			if loaded.GetTracing().Disable == nil || loaded.GetTracing().GetDisable() != tt.disabled {
				t.Fatalf("tracing disable = %v, want %t", loaded.GetTracing(), tt.disabled)
			}
			if loaded.GetTracing().GetExcludeQueryVars() != tt.local.GetExcludeQueryVars() {
				t.Fatalf("local tracing fields were changed: %v", loaded.GetTracing())
			}
			if loaded.GetDefault() != "default" || loaded.GetGorm().GetLogger().GetSlowThreshold().AsDuration() != defaultConfig().GetGorm().GetLogger().GetSlowThreshold().AsDuration() {
				t.Fatalf("database defaults were changed: %v", loaded)
			}
		})
	}
	if template := defaultConfig(); template.GetTracing() != nil || template.GetMetrics() != nil {
		t.Fatalf("environment-independent template was changed: %v", template)
	}
}

func TestLoadConfigPreservesConfigReadErrors(t *testing.T) {
	cause := errors.New("source unavailable")
	for _, key := range []string{"tracing.disable", "database"} {
		t.Run(key, func(t *testing.T) {
			_, err := loadConfig(loadErrorDatabaseConfigManager{Manager: testconfig.Empty(t), key: key, err: cause}, nil)
			if !errors.Is(err, cause) {
				t.Fatalf("loadConfig error = %v, want source cause", err)
			}
		})
	}
}

func TestValidateDatabaseConfigUsesDriverSnapshot(t *testing.T) {
	config := &config_pb.Database{
		Default: proto.String("default"),
		Connections: map[string]*config_pb.DBConnection{
			"default": {
				Driver: proto.String("postgres"),
				Dsn:    "fixture-dsn",
			},
		},
	}

	err := validateDatabaseConfig(config, map[string]DriverFactory{"mysql": validationStubDriver})
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateDatabaseConfigAcceptsNormalizedRegisteredDriver(t *testing.T) {
	config := &config_pb.Database{
		Default: proto.String("default"),
		Connections: map[string]*config_pb.DBConnection{
			"default": {
				Driver: proto.String(" MYSQL "),
				Dsn:    "fixture-dsn",
			},
		},
	}

	if err := validateDatabaseConfig(config, map[string]DriverFactory{"mysql": validationStubDriver}); err != nil {
		t.Fatal(err)
	}
}
