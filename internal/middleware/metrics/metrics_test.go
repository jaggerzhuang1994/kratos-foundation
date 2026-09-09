package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationmetrics "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestRequestDurationHistogramUsesPrometheusBaseName(t *testing.T) {
	tests := []struct {
		name       string
		newContext func(context.Context) context.Context
		newMetric  func(foundationmetrics.Provider) (middleware.Middleware, error)
		prefix     string
	}{
		{
			name: "server",
			newContext: func(ctx context.Context) context.Context {
				return transport.NewServerContext(ctx, new(kratoshttp.Transport))
			},
			newMetric: func(provider foundationmetrics.Provider) (middleware.Middleware, error) {
				return Server(provider, nil)
			},
			prefix: "server_requests_seconds",
		},
		{
			name: "client",
			newContext: func(ctx context.Context) context.Context {
				return transport.NewClientContext(ctx, new(kratoshttp.Transport))
			},
			newMetric: func(provider foundationmetrics.Provider) (middleware.Middleware, error) {
				return Client(provider, nil)
			},
			prefix: "client_requests_seconds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, cleanup, err := foundationmetrics.NewProvider(appinfo.New("test"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			metricMiddleware, err := test.newMetric(provider)
			if err != nil {
				t.Fatal(err)
			}
			handler := metricMiddleware(func(context.Context, any) (any, error) {
				return nil, nil
			})
			if _, err := handler(test.newContext(context.Background()), nil); err != nil {
				t.Fatal(err)
			}

			recorder := httptest.NewRecorder()
			promhttp.HandlerFor(provider.PrometheusGatherer(), promhttp.HandlerOpts{}).ServeHTTP(
				recorder,
				httptest.NewRequest("GET", "/metrics", nil),
			)
			body := recorder.Body.String()
			if !strings.Contains(body, test.prefix+"_bucket{") {
				t.Fatalf("Prometheus output does not contain %q:\n%s", test.prefix+"_bucket", body)
			}
			if strings.Contains(body, test.prefix+"_bucket_bucket{") {
				t.Fatalf("Prometheus output contains duplicate bucket suffix:\n%s", body)
			}
		})
	}
}
