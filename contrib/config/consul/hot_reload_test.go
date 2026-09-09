package consul

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestConsulOutageAndDeletionRecoverExistingHotReloadValue(t *testing.T) {
	var outage, deleted atomic.Bool
	watching, changed, failed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var watchingOnce sync.Once
	remote, _ := consulTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("index") {
		case "1":
			watchingOnce.Do(func() { close(watching) })
			select {
			case <-changed:
			case <-r.Context().Done():
				return
			}
		case "2":
			<-r.Context().Done()
			return
		}
		if outage.Load() {
			select {
			case failed <- struct{}{}:
			default:
			}
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		values := consulapi.KVPairs{{Key: "settings/feature.json", Value: []byte(`{"feature":{"enabled":true}}`), ModifyIndex: 1}}
		w.Header().Set("X-Consul-Index", "1")
		if deleted.Load() {
			values = consulapi.KVPairs{}
			w.Header().Set("X-Consul-Index", "2")
		}
		_ = json.NewEncoder(w).Encode(values)
	})
	base, err := text.NewSource("base.json", "json", `{"feature":{"enabled":false}}`)
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := foundationconfig.NewManager(foundationconfig.Sources{base, remote})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	type feature struct {
		Enabled bool `json:"enabled"`
	}
	hot, stop, err := foundationconfig.NewHotReloadValue[feature](manager, "feature")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	select {
	case <-watching:
	case <-time.After(time.Second):
		t.Fatal("blocking query did not start")
	}
	outage.Store(true)
	deleted.Store(true)
	close(changed)
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("outage was not observed")
	}
	if value, _ := hot.GetCurrent(); !value.Enabled {
		t.Fatal("outage discarded the valid remote snapshot")
	}
	if err := manager.Load("feature", new(feature)); err != nil {
		t.Fatalf("manager became terminal during outage: %v", err)
	}
	outage.Store(false)
	deadline := time.After(2 * time.Second)
	for {
		if value, _ := hot.GetCurrent(); !value.Enabled {
			break
		}
		select {
		case <-deadline:
			t.Fatal("deleted override did not reveal base value after recovery")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
