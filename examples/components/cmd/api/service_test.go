package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/v9"
)

func TestDemoHTTP(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	logger, closeLog, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLog)
	info := appinfo.New("test")
	provider, closeMetrics, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeMetrics)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"hello from Foundation"}`))
	}))
	t.Cleanup(upstream.Close)
	components := httptest.NewUnstartedServer(nil)
	t.Cleanup(components.Close)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("tracing:\n  disable: true\nclient:\n  clients:\n    greeting:\n      protocol: HTTP\n      target: "+upstream.URL+"\n    components:\n      protocol: HTTP\n      target: http://"+components.Listener.Addr().String()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := newSources(logger, configPath(path))
	if err != nil {
		t.Fatal(err)
	}
	cfg, closeCfg, err := config.NewManager(sources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeCfg)
	tracingProvider, closeTracing, err := tracing.NewProvider(cfg, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTracing)
	factory, closeClient, err := client.NewFactory(cfg, logger, info, tracingProvider, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	cache := redis.NewClient(&redis.Options{Addr: "unused"})
	cache.AddHook(&demoRedisReadHook{})
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Error(err)
		}
	})
	svc, err := newDemoService(&dataService{cache: cache}, &objectStorage{}, &messaging{}, factory, logger, provider)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	svc.steps = []demoStep{{"record", func(ctx context.Context, id string) error { ids = append(ids, id); return nil }}}
	spec := server.NewSpec()
	spec.GRPC().Disable()
	var handler server.HTTPServer
	spec.HTTP().Health(server.HealthConfig{Disable: true}).Register(func(s server.HTTPServer) error { handler = s; svc.register(s); return nil })
	_, closeServer, err := server.NewRuntime(cfg, logger, provider, tracingProvider, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeServer)
	components.Config.Handler = handler
	components.Start()
	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/demo/run", nil))
		if response.Code != 200 {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("run IDs not isolated: %v", ids)
	}
	svc.steps = []demoStep{{"failure", func(context.Context, string) error { return errors.New("expected failure") }}}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/demo/run", nil))
	if response.Code != 500 {
		t.Fatalf("failure status=%d", response.Code)
	}
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	totals := map[string]float64{}
	for _, f := range families {
		found[f.GetName()] = true
		for _, sample := range f.Metric {
			labels := map[string]string{}
			for _, pair := range sample.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if sample.Counter != nil {
				totals[f.GetName()+":"+labels["operation"]] += sample.Counter.GetValue()
			}
		}
		if f.GetName() == "business_demo_runs_total" && f.Metric[0].GetCounter().GetValue() != 2 {
			t.Fatal("failed run counted as success")
		}
	}
	for _, name := range []string{"business_demo_runs_total", "client_requests_code_total", "server_requests_code_total"} {
		if !found[name] {
			t.Errorf("missing %s", name)
		}
	}
	for key, want := range map[string]float64{
		"server_requests_code_total:/example.Components/Run":       3,
		"server_requests_code_total:/example.Components/Catalog":   2,
		"server_requests_code_total:/example.Components/Inventory": 2,
		"client_requests_code_total:/example.Greeting/Hello":       2,
		"client_requests_code_total:/example.Components/Catalog":   2,
		"client_requests_code_total:/example.Components/Inventory": 2,
	} {
		if totals[key] != want {
			t.Errorf("%s=%v want %v", key, totals[key], want)
		}
	}

}

// 只替代 Redis 网络调用；HTTP 和 client/server 指标仍走实际运行时。
type demoRedisReadHook struct{ orderCacheHook }

func (demoRedisReadHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "ping":
			cmd.(*redis.StatusCmd).SetVal("PONG")
		case "dbsize":
			cmd.(*redis.IntCmd).SetVal(3)
		case "ttl":
			cmd.(*redis.DurationCmd).SetVal(-2)
		default:
			return errors.New("unexpected read command")
		}
		return nil
	}
}
