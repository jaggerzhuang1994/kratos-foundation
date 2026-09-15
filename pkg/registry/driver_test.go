package registry

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"testing"
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
