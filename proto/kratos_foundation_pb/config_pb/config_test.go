package config_pb

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestModuleLogGeneratedValidationAcceptsOnlyDocumentedLevels(t *testing.T) {
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
			config := &ModuleLog{Level: proto.String(test.level)}
			err := config.ValidateAll()
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateAll() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
