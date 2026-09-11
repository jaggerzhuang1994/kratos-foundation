package bootstrap_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	consulapi "github.com/hashicorp/consul/api"
	consulconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/consul"
	fileconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestConsulSourcesPreserveLayerPriority(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("index") != "" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("X-Consul-Index", "1")
		key := "common/config.yaml"
		value := "remote-common"
		if r.URL.Path == "/v1/kv/app" {
			key, value = "app/config.yaml", "remote-app"
		}
		if err := json.NewEncoder(w).Encode(consulapi.KVPairs{&consulapi.KVPair{Key: key, Value: []byte("value: " + value)}}); err != nil {
			t.Errorf("write Consul response: %v", err)
		}
	}))
	defer remote.Close()
	client, err := consulapi.NewClient(&consulapi.Config{Address: remote.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"local", "prod"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			info := appinfo.New("test")
			dir := t.TempDir()
			for name, value := range map[string]string{
				"config.yaml":                             "base",
				info.Name() + ".yaml":                     "app",
				environment + ".config.yaml":              "environment",
				environment + "." + info.Name() + ".yaml": "local-app",
				"ignored.yaml":                            "unselected",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("value: "+value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			logger, _, _ := newTestObservability(t)
			paths := fileconfig.PathList{
				filepath.Join(dir, "config.yaml"),
				filepath.Join(dir, info.Name()+".yaml"),
				filepath.Join(dir, environment+".config.yaml"),
				filepath.Join(dir, environment+"."+info.Name()+".yaml"),
			}
			files, err := fileconfig.NewSources(logger, paths)
			if err != nil {
				t.Fatal(err)
			}
			remoteSources, err := consulconfig.NewSources(client, logger, consulconfig.PathList{"common", "app"})
			if err != nil {
				t.Fatal(err)
			}
			sources := bootstrap.NewConsulSources(files, remoteSources)
			if len(sources) != 6 {
				t.Fatalf("sources = %d, want 4 files and 2 Consul paths", len(sources))
			}
			manager, cleanup, err := config.NewManager(sources)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			var value string
			if err := manager.Load("value", &value); err != nil {
				t.Fatal(err)
			}
			want := "remote-app"
			if environment == "local" {
				want = "local-app"
			}
			if value != want {
				t.Fatalf("value = %q, want %q", value, want)
			}
		})
	}
}
