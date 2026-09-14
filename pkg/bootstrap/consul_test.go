package bootstrap_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestRemoteConfigSourcesSelectEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	sources, err := bootstrap.NewRemoteConfigSources(nil, nil)
	if err != nil || len(sources) != 0 {
		t.Fatalf("local = %v, %v", sources, err)
	}
	t.Setenv("APP_ENV", "prod")
	if _, err := bootstrap.NewRemoteConfigSources(nil, bootstrap.RemoteConfigPaths{"configs/common.yaml"}); err == nil {
		t.Fatal("disabled remote accepted")
	}
	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.NewRemoteConfigSources(client, nil); err == nil {
		t.Fatal("empty remote accepted")
	}
	sources, err = bootstrap.NewRemoteConfigSources(client, bootstrap.RemoteConfigPaths{"configs/common.yaml", "configs/app/*.yaml"})
	if err != nil || len(sources) != 2 {
		t.Fatalf("remote = %v, %v", sources, err)
	}
}

func TestRemoteConfigEightLayersLoadInOrder(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	paths, err := bootstrap.NewRemoteConfigPaths("orders")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("index") != "" {
			<-r.Context().Done()
			return
		}
		requested := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
		for index, path := range paths {
			if requested != strings.TrimSuffix(path, "*.yaml") {
				continue
			}
			key := path
			if strings.HasSuffix(path, "*.yaml") {
				key = strings.TrimSuffix(path, "*.yaml") + "app.yaml"
			}
			w.Header().Set("X-Consul-Index", "1")
			if err := json.NewEncoder(w).Encode(consulapi.KVPairs{{Key: key, Value: []byte(fmt.Sprintf("value: %d\nlayer%d: true", index, index))}}); err != nil {
				t.Error(err)
			}
			return
		}
		http.Error(w, "unexpected path", http.StatusBadRequest)
	}))
	defer server.Close()
	client, err := consulapi.NewClient(&consulapi.Config{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := bootstrap.NewRemoteConfigSources(client, paths)
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := config.NewManager(bootstrap.NewConfigSources(nil, sources))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value int
	if err := manager.Load("value", &value); err != nil || value != 7 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	for index := range paths {
		var present bool
		if err := manager.Load(fmt.Sprintf("layer%d", index), &present); err != nil || !present {
			t.Fatalf("layer%d missing: %v", index, err)
		}
	}
}
