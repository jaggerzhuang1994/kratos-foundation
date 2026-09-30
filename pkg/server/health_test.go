package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/request"
	"github.com/prometheus/client_golang/prometheus"
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

func TestReadinessTransitionLogsProbeContextWithoutDependencyDetails(t *testing.T) {
	type probeKey struct{}
	logger, path := newBoundaryTestLogger(t, kratoslog.LevelDebug)
	logger = logger.With("probe", kratoslog.Valuer(func(ctx context.Context) any { return ctx.Value(probeKey{}) }))
	restore := foundationlog.SetLogger(logger)
	defer restore()
	health := newHealthState(healthConfig{Timeout: time.Second, ReadinessPath: "/readyz", Checks: []ReadinessCheck{
		{Name: "database", Check: func(context.Context) error { return errors.New("private database credential") }},
	}})
	health.applicationReady = func() bool { return true }
	ctx := request.WithDebug(context.WithValue(context.Background(), probeKey{}, "probe-a"))
	for range 2 {
		if health.ready(ctx) {
			t.Fatal("failed dependency accepted")
		}
	}
	health.config.Checks[0].Check = func(context.Context) error { return nil }
	if !health.ready(ctx) {
		t.Fatal("healthy dependency rejected")
	}
	health.config.Checks[0].Check = func(context.Context) error {
		health.applicationReady = func() bool { return false }
		return nil
	}
	if health.ready(ctx) {
		t.Fatal("application readiness change during dependency check was lost")
	}
	line := logDelta(t, path, 0)
	if strings.Count(line, "event=server.readiness.changed") != 3 || strings.Contains(line, "private database credential") {
		t.Fatalf("readiness transitions duplicated or disclosed dependency details: %s", line)
	}
	if strings.Count(line, "check=database") != 1 {
		t.Fatalf("application readiness change was attributed to a successful dependency: %s", line)
	}
	for _, field := range []string{"transport=http", "path=/readyz", "probe=probe-a", "check=database", "status=503", "status=200", "reason=dependency_failed", "reason=application_not_ready", "DEBUG ", "WARN ", "INFO "} {
		if !strings.Contains(line, field) {
			t.Fatalf("readiness diagnostic missing %s: %s", field, line)
		}
	}
	for _, tc := range []struct{ reason, level string }{
		{reason: "application_not_ready", level: "DEBUG"},
		{reason: "server_stopping", level: "DEBUG"},
		{reason: "probe_canceled", level: "DEBUG"},
		{reason: "probe_timeout", level: "WARN"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			before := logSize(t, path)
			health := newHealthState(healthConfig{Timeout: time.Second, ReadinessPath: "/readyz"})
			if tc.reason != "application_not_ready" {
				health.applicationReady = func() bool { return true }
			}
			probeContext := ctx
			switch tc.reason {
			case "server_stopping":
				health.stopped.Store(true)
			case "probe_canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				probeContext = canceled
			case "probe_timeout":
				expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Hour))
				defer cancel()
				probeContext = expired
			}
			if health.ready(probeContext) {
				t.Fatal("unready probe accepted")
			}
			line := logDelta(t, path, before)
			if !strings.Contains(line, "reason="+tc.reason) || !strings.HasPrefix(line, tc.level+" ") {
				t.Fatalf("readiness source classification = %s", line)
			}
		})
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
	if _, _, err := configureMonitoring(config, srv.HTTPServer, health, testMetricsProvider{registry: prometheus.NewRegistry()}); err != nil {
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
	if _, _, err := configureMonitoring(config, srv.HTTPServer, health, testMetricsProvider{registry: prometheus.NewRegistry()}); err == nil {
		t.Fatal("metrics conflict accepted")
	}
}
