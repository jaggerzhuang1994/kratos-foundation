package observability

import (
	"errors"
	"testing"

	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func TestLoadTracingDefaults(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     string
		disable *bool
		want    bool
	}{
		{name: "local default", env: "local", want: true},
		{name: "production default", env: "prod"},
		{name: "explicit enabled", env: "local", disable: proto.Bool(false)},
		{name: "explicit disabled", env: "prod", disable: proto.Bool(true), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", test.env)
			manager := testconfig.New(t, "tracing", &config_pb.Tracing{Disable: test.disable})
			defaults, err := Load(manager)
			if err != nil || defaults.TracingDisabled != test.want {
				t.Fatalf("Load = (%+v, %v), want disabled=%t", defaults, err, test.want)
			}
		})
	}
}

type failingReader struct{ err error }

func (r failingReader) Load(string, any, ...any) error { return r.err }

func TestLoadPreservesSourceErrorAndRejectsInvalidBoolean(t *testing.T) {
	cause := errors.New("source unavailable")
	if _, err := Load(failingReader{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("Load error = %v, want source error", err)
	}
	source, err := textconfig.NewSource("invalid", config.JSONFormat, `{"tracing":{"disable":"invalid"}}`)
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := Load(manager); err == nil {
		t.Fatal("invalid tracing.disable accepted")
	}
}
