package config_pb

import (
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestLogModuleGeneratedValidationAcceptsOnlyDocumentedLevels(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{name: "empty", level: "", wantErr: true},
		{name: "upper case", level: "INFO"},
		{name: "lower case", level: "debug"},
		{name: "unknown", level: "verbose", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := &LogModule{Module: "test", Level: proto.String(test.level)}
			err := config.ValidateAll()
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateAll() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

// 配置按 JSON 字段名解码，不依赖历史二进制字段编号。
func TestConfigJSONContract(t *testing.T) {
	t.Run("app", func(t *testing.T) {
		var value App
		if err := protojson.Unmarshal([]byte(`{"registrar_timeout":"10s","stop_timeout":"30s"}`), &value); err != nil {
			t.Fatal(err)
		}
		if value.GetRegistrarTimeout().AsDuration() != 10*time.Second || value.GetStopTimeout().AsDuration() != 30*time.Second {
			t.Fatal(&value)
		}
	})
	t.Run("server", func(t *testing.T) {
		var value Server
		if err := protojson.Unmarshal([]byte(`{"http":{"addr":":8000"},"grpc":{"addr":":9000"}}`), &value); err != nil {
			t.Fatal(err)
		}
		if value.GetHttp().GetAddr() != ":8000" || value.GetGrpc().GetAddr() != ":9000" {
			t.Fatal(&value)
		}
	})
	t.Run("database", func(t *testing.T) {
		var value Database
		if err := protojson.Unmarshal([]byte(`{"default":"main","connections":{"main":{"dsn":"database.db"}}}`), &value); err != nil {
			t.Fatal(err)
		}
		if value.GetDefault() != "main" || value.GetConnections()["main"].GetDsn() != "database.db" {
			t.Fatal(&value)
		}
	})
}
