package server

import (
	"context"
	"errors"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthEndpointsAndReadiness(t *testing.T) {
	checks := []ReadinessCheck{{Name: "database", Check: func(context.Context) error { return errors.New("secret database address") }}}
	spec := NewSpec()
	spec.Health().Checks(checks...)
	checks[0].Check = nil
	health := configuredHealth(defaultConfig, spec)
	if err := health.validate("/metrics"); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{health: health}
	ready := false
	runtime.SetReadinessSource(func() bool { return ready })
	handler := health.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	request := func(path, method string, want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
		if recorder.Code != want || recorder.Body.Len() != 0 {
			t.Fatalf("%s %s: code=%d body=%q", method, path, recorder.Code, recorder.Body.String())
		}
	}
	request("/healthz", "GET", 200)
	request("/readyz", "GET", 503)
	request("/orders", "GET", 401)
	request("/healthz", "POST", 405)
	ready = true
	request("/readyz", "GET", 503)
	health.config.Checks[0].Check = func(context.Context) error { return nil }
	request("/readyz", "HEAD", 200)
	health.config.Checks[0].Check = func(context.Context) error { ready = false; return nil }
	request("/readyz", "GET", 503)
	ready = true
	health.config.Checks = nil
	if err := withStopLifecycle(&testServer{}, 0, nil, health).Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	request("/readyz", "GET", 503)
	request("/healthz", "GET", 200)
}

func TestHealthValidationAndCancellation(t *testing.T) {
	for _, config := range []healthConfig{
		{LivenessPath: "bad"}, {LivenessPath: "/bad?query=x"}, {LivenessPath: "/%zz"},
		{LivenessPath: "/metrics"}, {ReadinessPath: "/healthz"}, {Timeout: -time.Second},
		{Checks: []ReadinessCheck{{Name: "db"}}},
		{Checks: []ReadinessCheck{{Name: "db", Check: func(context.Context) error { return nil }}, {Name: "db", Check: func(context.Context) error { return nil }}}},
	} {
		if err := newHealthState(config).validate("/metrics"); err == nil {
			t.Fatalf("accepted invalid config: %+v", config)
		}
	}
	disabled := newHealthState(healthConfig{Disable: true})
	if err := disabled.validate("/healthz"); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	recorder := httptest.NewRecorder()
	disabled.wrap(handler).ServeHTTP(recorder, httptest.NewRequest("GET", "/healthz", nil))
	if recorder.Code != 204 {
		t.Fatal("disabled health intercepted route")
	}
	health := newHealthState(healthConfig{Timeout: time.Millisecond, Checks: []ReadinessCheck{{Name: "wait", Check: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}}})
	health.applicationReady = func() bool { return true }
	if health.ready(context.Background()) {
		t.Fatal("timed out dependency accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if health.ready(ctx) {
		t.Fatal("canceled probe accepted")
	}
	if newHealthState(healthConfig{}).ready(context.Background()) {
		t.Fatal("unbound readiness accepted")
	}
}

func TestHTTPHealthPrecedesBusinessFiltersAndPrefix(t *testing.T) {
	config, err := loadConfig(testconfig.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	spec := NewSpec()
	spec.HTTP().Option(kratoshttp.PathPrefix("/api"), kratoshttp.Filter(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	}))
	health := configuredHealth(config, spec)
	health.applicationReady = func() bool { return true }
	srv, err := newHTTPServer(config, newHTTPServerOptions(config, nil, spec), spec, nil, newWebSocketHub())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := configureMonitoring(config, srv, health, testMetricsProvider{registry: prometheus.NewRegistry()}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want int
	}{{"/healthz", 200}, {"/readyz", 200}, {"/api/orders", 401}, {"/api/healthz", 401}} {
		response := httptest.NewRecorder()
		srv.ServeHTTP(response, httptest.NewRequest("GET", test.path, nil))
		if response.Code != test.want {
			t.Fatalf("%s=%d, want %d", test.path, response.Code, test.want)
		}
	}
	health.config.LivenessPath = config.GetHttp().GetMetrics().GetPath()
	if _, _, err := configureMonitoring(config, srv, health, testMetricsProvider{registry: prometheus.NewRegistry()}); err == nil {
		t.Fatal("metrics conflict accepted")
	}
}
