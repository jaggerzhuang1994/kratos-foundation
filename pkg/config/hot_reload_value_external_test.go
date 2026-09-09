package config_test

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"google.golang.org/protobuf/types/known/durationpb"
	"runtime"
	"testing"
	"time"
)

func TestHotReloadValueSupportsDottedDurationPath(t *testing.T) {
	source := newJSONSource(`{"app":{"timeout":"5s"}}`)
	manager, cleanup, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	hot, cancel, err := config.NewHotReloadValue[durationpb.Duration](manager, "app.timeout", durationpb.New(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	value, _ := hot.GetCurrent()
	if value.AsDuration() != 5*time.Second {
		t.Fatalf("initial duration = %v", value)
	}
	for _, tt := range []struct {
		content string
		want    time.Duration
	}{
		{`{"app":{"timeout":"7s"}}`, 7 * time.Second},
		{`{"app":{}}`, 3 * time.Second},
	} {
		source.publish(jsonValues(tt.content), jsonValues(tt.content))
		deadline := time.Now().Add(2 * time.Second)
		for {
			value, _ := hot.GetCurrent()
			if value.AsDuration() == tt.want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("duration = %v, want %v", value, tt.want)
			}
			runtime.Gosched()
		}
	}
}
