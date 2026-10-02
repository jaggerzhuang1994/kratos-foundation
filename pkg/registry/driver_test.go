package registry

import (
	"testing"

	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func TestDriverRegistrationFreeze(t *testing.T) {
	r := driverRegistry{factories: make(map[string]DriverFactory)}
	factory := func(DriverConfig, log.Logger) (Resource, func(), error) { return Resource{}, nil, nil }
	if err := r.register("test", factory); err != nil {
		t.Fatal(err)
	}
	if err := r.register("test", factory); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := r.register("", factory); err == nil {
		t.Fatal("empty accepted")
	}
	snapshot := r.snapshot()
	delete(snapshot, "test")
	if len(r.snapshot()) != 1 {
		t.Fatal("snapshot leaked map")
	}
	if err := r.register("next", factory); err == nil {
		t.Fatal("late registration accepted")
	}
	if err := RegisterDriver("", nil); err == nil {
		t.Fatal("invalid public registration accepted")
	}
}

func TestNullDriverFromYAML(t *testing.T) {
	for _, test := range []struct {
		name    string
		driver  string
		wantErr bool
	}{
		{name: "quoted driver", driver: `"null"`},
		{name: "yaml null value", driver: "null", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := textconfig.NewSource("test", config.YAMLFormat, "registry:\n  instances:\n    default:\n      driver: "+test.driver+"\n")
			if err != nil {
				t.Fatal(err)
			}
			manager, closeManager, err := config.NewManager(config.Sources{source})
			if err != nil {
				t.Fatal(err)
			}
			defer closeManager()
			factory, cleanup, err := NewFactory(manager, log.WithModule("test"))
			if test.wantErr {
				if err == nil {
					cleanup()
					t.Fatal("yaml null value accepted as a driver name")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			registrar, err := factory.Registrar("default")
			if err != nil || registrar != nil {
				t.Fatalf("null registrar = %v, %v; want nil, nil", registrar, err)
			}
			discovery, err := factory.Discovery("default")
			if err != nil || discovery != nil {
				t.Fatalf("null discovery = %v, %v; want nil, nil", discovery, err)
			}
		})
	}
}
