package registry

import (
	"context"
	"errors"
	"reflect"
	"testing"

	kratosregistry "github.com/go-kratos/kratos/v2/registry"
	textconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type testRegistrar struct{}

func (*testRegistrar) Register(context.Context, *kratosregistry.ServiceInstance) error   { return nil }
func (*testRegistrar) Deregister(context.Context, *kratosregistry.ServiceInstance) error { return nil }

func TestFactoryOwnershipAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			source, err := textconfig.NewSource("test", "json", `{"registry":{"instances":{"a":{"driver":"test","options":{"value":7}},"b":{"driver":"test"}}}}`)
			if err != nil {
				t.Fatal(err)
			}
			manager, closeManager, err := config.NewManager(config.Sources{source})
			if err != nil {
				t.Fatal(err)
			}
			defer closeManager()
			var closed []string
			factory, cleanup, err := newFactory(manager, log.WithModule("test"), map[string]DriverFactory{"test": func(c DriverConfig, _ log.Logger) (Resource, func(), error) {
				if c.Name == "a" {
					var value int
					if err := c.Load("value", &value); err != nil || value != 7 {
						t.Fatalf("scoped config: %d %v", value, err)
					}
				}
				if fail && c.Name == "b" {
					return Resource{}, nil, errors.New("failure")
				}
				return Resource{Registrar: &testRegistrar{}}, func() { closed = append(closed, c.Name) }, nil
			}})
			if fail {
				if err == nil || !reflect.DeepEqual(closed, []string{"a"}) {
					t.Fatalf("rollback: %v %v", err, closed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = factory.Registrar("a"); err != nil {
				t.Fatal(err)
			}
			if _, err = factory.Discovery("a"); err == nil {
				t.Fatal("missing capability accepted")
			}
			if _, err = factory.Registrar("missing"); err == nil {
				t.Fatal("missing instance accepted")
			}
			cleanup()
			cleanup()
			if !reflect.DeepEqual(closed, []string{"b", "a"}) {
				t.Fatal(closed)
			}
		})
	}
}

func TestNewFactoryEmpty(t *testing.T) {
	manager, closeManager, err := config.NewManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeManager()
	factory, cleanup, err := NewFactory(manager, log.WithModule("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := factory.Discovery("missing"); err == nil {
		t.Fatal("missing instance accepted")
	}
}

func TestFactoryDistinguishesDisabledFromMissingCapability(t *testing.T) {
	source, err := textconfig.NewSource("test", "json", `{"registry":{"instances":{"main":{"driver":"test"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManager()
	for _, disabled := range []bool{false, true} {
		factory, cleanup, err := newFactory(manager, log.WithModule("test"), map[string]DriverFactory{"test": func(DriverConfig, log.Logger) (Resource, func(), error) {
			return Resource{Disabled: disabled}, nil, nil
		}})
		if !disabled {
			if err == nil {
				cleanup()
				t.Fatal("empty enabled resource accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		registrar, err := factory.Registrar("main")
		if err != nil || registrar != nil {
			t.Fatalf("disabled registrar: %v", err)
		}
		discovery, err := factory.Discovery("main")
		if err != nil || discovery != nil {
			t.Fatalf("disabled discovery: %v", err)
		}
		cleanup()
	}
}
