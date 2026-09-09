package bootstrap_test

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
)

func TestConfigMetricsAreInstanceScopedAndCleanupUnregisters(t *testing.T) {
	manager, cleanupManager, err := config.NewManager(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupManager()
	provider, cleanupProvider, err := metrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupProvider()
	_, cleanup, err := bootstrap.NewConfigObservabilityBootstrap(manager, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, _, err := bootstrap.NewConfigObservabilityBootstrap(manager, provider); err == nil {
		t.Fatal("duplicate collector accepted")
	}
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "foundation_config_watcher_up" {
			found = true
			if family.Metric[0].GetGauge().GetValue() != 1 {
				t.Fatal("watcher not up")
			}
		}
	}
	if !found {
		t.Fatal("config metric not registered")
	}
	cleanup()
	cleanup()
	families, err = provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "foundation_config_watcher_up" {
			t.Fatal("collector not unregistered")
		}
	}
	if _, _, err := bootstrap.NewConfigObservabilityBootstrap(managerWithoutStatus{manager}, provider); err == nil {
		t.Fatal("unsupported manager accepted")
	}
}

type managerWithoutStatus struct{ config.Manager }
