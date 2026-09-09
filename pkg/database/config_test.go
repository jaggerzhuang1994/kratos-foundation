package database

import (
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func validationStubDriver(DriverConfig) (DriverConnection, error) {
	return DriverConnection{}, nil
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
