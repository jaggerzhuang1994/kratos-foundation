package consul

import (
	"encoding/json"
	configconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	baseconsul "github.com/jaggerzhuang1994/kratos-foundation/v2/internal/consul"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	configtext "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
)

func TestRegisteredConsulDriver(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status/leader" {
			http.NotFound(w, r)
			return
		}
		probes.Add(1)
		if err := json.NewEncoder(w).Encode("127.0.0.1:8300"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("CONSUL_HTTP_ADDR", server.URL)
	t.Setenv("DISABLE_CONSUL", "false")
	data, err := json.Marshal(map[string]any{"registry": map[string]any{"instances": map[string]any{
		"main": map[string]any{"driver": "consul", "options": map[string]any{}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := configtext.NewSource("test", "json", string(data))
	if err != nil {
		t.Fatal(err)
	}
	manager, closeManager, err := config.NewManager(config.Sources{source})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManager()
	factory, cleanup, err := registry.NewFactory(manager, log.WithModule("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// 配置源和注册发现均借用同一客户端，KV 缺失可形成空配置快照。
	sources, err := configconsul.AddConfigSource("configs/app.yaml")()
	if err != nil {
		t.Fatal(err)
	}
	_, closeSources, err := config.NewManager(sources)
	if err != nil {
		t.Fatal(err)
	}
	closeSources()
	shared, disabled, err := baseconsul.Get()
	if err != nil || disabled {
		t.Fatal(err)
	}
	registration, err := factory.Registrar("main")
	if err != nil {
		t.Fatal(err)
	}
	found, err := factory.Discovery("main")
	if err != nil {
		t.Fatal(err)
	}
	if registration.(*registrar).client != shared || found.(*discovery).client != shared || probes.Load() != 1 {
		t.Fatal("drivers did not share the singleton")
	}

	if _, err := factory.Registrar("main"); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Discovery("main"); err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()
	if _, err := shared.Status().Leader(); err != nil {
		t.Fatalf("driver cleanup invalidated shared client: %v", err)
	}
}

func TestDriverRejectsConnectionOptions(t *testing.T) {
	for _, value := range []string{`{}`, `null`, `{"address":"unused"}`} {
		source, err := configtext.NewSource("test", "json", `{"registry":{"instances":{"main":{"driver":"consul","options":{"connection":`+value+`}}}}}`)
		if err != nil {
			t.Fatal(err)
		}
		manager, closeManager, err := config.NewManager(config.Sources{source})
		if err != nil {
			t.Fatal(err)
		}
		_, cleanup, err := registry.NewFactory(manager, log.WithModule("test"))
		if cleanup != nil {
			cleanup()
		}
		closeManager()
		if err == nil {
			t.Fatal("removed connection option accepted")
		}
	}
}
