package metrics

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
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

func TestMetricsPreserveErrorStatusAndOriginalError(t *testing.T) {
	for _, side := range []string{"server", "client"} {
		for _, test := range []struct {
			name string
			err  error
			code string
		}{
			{name: "validator", err: foundationerrors.New(422, "VALIDATOR", "invalid input"), code: "422"},
			{name: "unknown", err: errors.New("private database failure"), code: "500"},
			{name: "canceled", err: context.Canceled, code: "499"},
			{name: "success", code: "200"},
		} {
			t.Run(side+"/"+test.name, func(t *testing.T) {
				provider, cleanup, err := foundationmetrics.NewProvider(appinfo.New("test"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanup)
				var metric middleware.Middleware
				var ctx context.Context
				if side == "server" {
					metric, err = Server(provider, nil)
					ctx = transport.NewServerContext(context.Background(), new(kratoshttp.Transport))
				} else {
					metric, err = Client(provider, nil)
					ctx = transport.NewClientContext(context.Background(), new(kratoshttp.Transport))
				}
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				reply, gotErr := metric(func(context.Context, any) (any, error) {
					calls++
					return "reply", test.err
				})(ctx, nil)
				if gotErr != test.err || reply != "reply" || calls != 1 {
					t.Fatalf("handler result changed: reply=%v err=%v calls=%d", reply, gotErr, calls)
				}
				families, err := provider.PrometheusGatherer().Gather()
				if err != nil {
					t.Fatal(err)
				}
				for _, family := range families {
					if family.GetName() != side+"_requests_code_total" {
						continue
					}
					for _, sample := range family.GetMetric() {
						for _, label := range sample.GetLabel() {
							if label.GetName() == "code" && label.GetValue() == test.code && sample.GetCounter().GetValue() == 1 {
								return
							}
						}
					}
				}
				t.Fatalf("request counter did not contain code %s", test.code)
			})
		}
	}
}
