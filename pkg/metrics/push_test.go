package metrics_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// pushRequest 记录假 Pushgateway 收到的一次推送。
type pushRequest struct {
	method, path, user, password string
	families                     map[string]*dto.MetricFamily
}

// newPushgateway 启动按 status 应答的假 Pushgateway，并解析 protobuf 分隔格式的请求体。
func newPushgateway(t *testing.T, status int) (string, <-chan pushRequest) {
	t.Helper()
	requests := make(chan pushRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received := pushRequest{method: r.Method, path: r.URL.Path, families: map[string]*dto.MetricFamily{}}
		received.user, received.password, _ = r.BasicAuth()
		decoder := expfmt.NewDecoder(r.Body, expfmt.ResponseFormat(r.Header))
		for {
			family := new(dto.MetricFamily)
			if err := decoder.Decode(family); err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("decode pushed metrics: %v", err)
				}
				break
			}
			received.families[family.GetName()] = family
		}
		requests <- received
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

func newPushProvider(t *testing.T) (metrics.Provider, appinfo.AppInfo) {
	t.Helper()
	info := appinfo.New("v1")
	provider, cleanup, err := metrics.NewProvider(info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return provider, info
}

func pushConfig(url, job string, timeout time.Duration) *config_pb.Metrics {
	push := &config_pb.Push{Url: proto.String(url)}
	if job != "" {
		push.Job = proto.String(job)
	}
	if timeout != 0 {
		push.Timeout = durationpb.New(timeout)
	}
	return &config_pb.Metrics{Push: push}
}

func TestNewPusherConfig(t *testing.T) {
	provider, info := newPushProvider(t)
	t.Run("disabled without url", func(t *testing.T) {
		for name, manager := range map[string]config.Manager{
			"missing section": testconfig.Empty(t),
			"empty url":       testconfig.New(t, "metrics", pushConfig("", "batch", 0)),
		} {
			pusher, err := metrics.NewPusher(manager, provider, info)
			if err != nil || pusher != nil {
				t.Fatalf("%s: pusher = %+v, err = %v, want disabled", name, pusher, err)
			}
		}
	})
	t.Run("defaults job to app name", func(t *testing.T) {
		pusher, err := metrics.NewPusher(testconfig.New(t, "metrics", pushConfig("pushgateway:9091", "", 0)), provider, info)
		if err != nil {
			t.Fatal(err)
		}
		if pusher.Job() != info.Name() {
			t.Fatalf("job = %q, want app name %q", pusher.Job(), info.Name())
		}
	})
	tests := []struct {
		name   string
		config *config_pb.Metrics
		want   string
	}{
		{"non-positive timeout", pushConfig("http://pushgateway:9091", "", -time.Second), "timeout must be positive"},
		{"missing host", pushConfig("http://user:secret@/", "", 0), "must be an absolute"},
		{"malformed url", pushConfig("http://user:secret@[::1", "", 0), "must be an absolute"},
		{"unsupported scheme", pushConfig("ftp://user:secret@pushgateway:9091", "", 0), "must be an absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := metrics.NewPusher(testconfig.New(t, "metrics", tt.config), provider, info)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaks credentials: %v", err)
			}
		})
	}
}

// 推送内容与 /metrics 共享同一 Registry；任务指标的 job 标签改为 exported_job，抓取侧保持原样。
func TestPusherPushesInstanceRegistry(t *testing.T) {
	provider, info := newPushProvider(t)
	counter, err := provider.Meter("test").Int64Counter("job_runs_total")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("job", "sync"), attribute.String("status", "success")))

	url, requests := newPushgateway(t, http.StatusOK)
	withAuth := strings.Replace(url, "http://", "http://pusher:secret@", 1)
	pusher, err := metrics.NewPusher(testconfig.New(t, "metrics", pushConfig(withAuth, "", 0)), provider, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := pusher.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := <-requests
	if got.method != http.MethodPut || got.path != "/metrics/job/"+info.Name() {
		t.Fatalf("request = %s %s, want PUT /metrics/job/%s", got.method, got.path, info.Name())
	}
	if got.user != "pusher" || got.password != "secret" {
		t.Fatalf("basic auth = %q/%q", got.user, got.password)
	}
	runs := got.families["job_runs_total"]
	if runs == nil || len(runs.GetMetric()) != 1 {
		t.Fatalf("job_runs_total not pushed: %v", runs)
	}
	labels := map[string]string{}
	for _, label := range runs.GetMetric()[0].GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	if _, ok := labels["job"]; ok || labels["exported_job"] != "sync" || labels["status"] != "success" {
		t.Fatalf("pushed labels = %v, want exported_job=sync without job", labels)
	}
	if got.families["process_cpu_seconds_total"] == nil && got.families["go_goroutines"] == nil {
		t.Fatal("native collectors from the shared registry were not pushed")
	}

	scraped, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range scraped {
		if family.GetName() != "job_runs_total" {
			continue
		}
		for _, label := range family.GetMetric()[0].GetLabel() {
			if label.GetName() == "exported_job" {
				t.Fatal("push rename leaked into /metrics scrape")
			}
		}
	}
}

func TestPusherReportsFailures(t *testing.T) {
	provider, info := newPushProvider(t)
	t.Run("rejected status", func(t *testing.T) {
		url, requests := newPushgateway(t, http.StatusBadRequest)
		pusher, err := metrics.NewPusher(testconfig.New(t, "metrics", pushConfig(url, "batch", 0)), provider, info)
		if err != nil {
			t.Fatal(err)
		}
		err = pusher.Push(context.Background())
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Fatalf("err = %v, want status 400", err)
		}
		if got := <-requests; got.path != "/metrics/job/batch" {
			t.Fatalf("path = %q, want configured job", got.path)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(server.Close)
		t.Cleanup(func() { close(release) })
		pusher, err := metrics.NewPusher(testconfig.New(t, "metrics", pushConfig(server.URL, "", 50*time.Millisecond)), provider, info)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if err := pusher.Push(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("push took %s, timeout not applied", elapsed)
		}
	})
}
