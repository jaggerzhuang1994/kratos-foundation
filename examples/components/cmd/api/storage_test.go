package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

func TestObjectStorageSDKMetrics(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%t", corrupt), func(t *testing.T) {
			t.Setenv("LOG_FILE_ENABLE", "false")
			logger, closeLog, err := log.NewLogger()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeLog)
			provider, closeMetrics, err := metrics.NewProvider(appinfo.New("oss-demo-test"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeMetrics)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/assets-emulated/components/test.txt" {
					http.Error(w, "invalid object path", 400)
					return
				}
				w.Header().Set("ETag", `"demo"`)
				w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
				switch r.Method {
				case http.MethodPut:
					body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
					if err != nil || string(body) != objectPayload {
						http.Error(w, "invalid upload", 400)
						return
					}
				case http.MethodGet, http.MethodHead:
					body := objectPayload
					if corrupt && r.Method == http.MethodGet {
						body = strings.Repeat("x", len(body))
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					if r.Method == http.MethodGet {
						if _, err := io.WriteString(w, body); err != nil {
							t.Error(err)
						}
					}
				case http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "invalid method", http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(endpoint.Close)
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := fmt.Sprintf("oss:\n  buckets:\n    assets:\n      driver: aliyun\n      bucket: assets-emulated\n      options:\n        region: cn-hangzhou\n        endpoint: %q\n        access_key_id: demo-only\n        access_key_secret: demo-only\n", endpoint.URL)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, closeConfig, err := config.NewManager(config.NewSources(file.NewSource(path)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeConfig)
			storage, cleanup, err := newObjectStorage(cfg, logger, provider)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			err = storage.Run(context.Background(), "test")
			if (err != nil) != corrupt {
				t.Fatalf("Run: %v", err)
			}
			families, err := provider.PrometheusGatherer().Gather()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]float64{}
			for _, family := range families {
				for _, metric := range family.Metric {
					operation := ""
					result := ""
					for _, label := range metric.Label {
						if label.GetName() == "result" {
							result = label.GetValue()
						}
						if label.GetName() == "operation" {
							operation = label.GetValue()
						}
					}
					got[family.GetName()+"/"+operation] += metric.GetCounter().GetValue()
					if family.GetName() == "oss_streams_total" && result != "success" {
						t.Errorf("stream result = %q", result)
					}
				}
			}
			want := map[string]float64{
				"oss_requests_total/put": 1, "oss_requests_total/stat": 1, "oss_requests_total/exists": 1,
				"oss_requests_total/get": 1, "oss_requests_total/delete": 1, "oss_streams_total/get": 1,
				"oss_transferred_bytes_total/put": float64(len(objectPayload)), "oss_transferred_bytes_total/get": float64(len(objectPayload)),
			}
			for name, value := range want {
				if got[name] != value {
					t.Errorf("%s = %v, want %v", name, got[name], value)
				}
			}
		})
	}
}
