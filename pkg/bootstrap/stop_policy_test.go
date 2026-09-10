package bootstrap

import (
	"io"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
)

func TestStopPolicyUsesComponentDelay(t *testing.T) {
	for _, tt := range []struct {
		name      string
		delay     time.Duration
		wantError bool
	}{
		{"worker", 0, false},
		{"server", time.Second, false},
		{"no shutdown budget", 30 * time.Second, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := testconfig.Empty(t)
			cfg, err := app.NewConfig(manager)
			if err != nil {
				t.Fatal(err)
			}
			policy, cleanup, err := NewStopPolicy(cfg, manager, kratoslog.NewStdLogger(io.Discard), ComponentsBootstrap{stopDelay: tt.delay})
			if tt.wantError {
				if err == nil || policy != nil || cleanup != nil {
					t.Fatal("invalid shutdown budget was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if policy == nil || cleanup == nil {
				t.Fatal("missing policy or cleanup")
			}
			cleanup()
		})
	}
}
