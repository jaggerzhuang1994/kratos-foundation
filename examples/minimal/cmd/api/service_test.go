package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
)

func TestHTTPExportsDashboardMetrics(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	logger, closeLog, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLog)
	provider, closeMetrics, err := metrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeMetrics)
	sources, err := newSources(logger, "../../configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manager, closeConfig, err := config.NewManager(sources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeConfig)
	traceProvider, closeTracing, err := tracing.NewProvider(manager, appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeTracing)
	service, err := newGreetingService(provider)
	if err != nil {
		t.Fatal(err)
	}
	spec := server.NewSpec()
	spec.GRPC().Disable()
	var httpServer server.HTTPServer
	spec.HTTP().Health(server.HealthConfig{Disable: true}).Register(func(srv server.HTTPServer) error {
		httpServer = srv
		service.register(srv)
		return nil
	})
	_, closeServer, err := server.NewRuntime(manager, logger, provider, traceProvider, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeServer)
	response := httptest.NewRecorder()
	httpServer.ServeHTTP(response, httptest.NewRequest("GET", "/hello", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "hello from Foundation") {
		t.Fatalf("hello: %d %s", response.Code, response.Body.String())
	}
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, family := range families {
		found[family.GetName()] = true
		if family.GetName() == "server_requests_code_total" {
			labels := map[string]string{}
			for _, label := range family.Metric[0].Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["operation"] != "/example.Greeting/Hello" || labels["code"] != "200" {
				t.Fatalf("request labels: %v", labels)
			}
		}
	}
	for _, name := range []string{"server_requests_code_total", "server_requests_seconds", "business_greetings_total", "go_goroutines", "process_resident_memory_bytes"} {
		if !found[name] {
			t.Errorf("missing dashboard metric %s", name)
		}
	}
}
