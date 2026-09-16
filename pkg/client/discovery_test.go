package client

import (
	"fmt"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"testing"
)

type testResolver map[string]registry.Discovery

func (r testResolver) Discovery(name string) (registry.Discovery, error) {
	d, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("unknown %s", name)
	}
	return d, nil
}
func TestNamedDiscoveryValidation(t *testing.T) {
	b := newTestRealBuilder(t, nil)
	b.discoveries = testResolver{}
	if err := b.validateConfig(&config_pb.Client{Clients: map[string]*config_pb.ClientOption{"orders": {Discovery: "missing"}}}); err == nil {
		t.Fatal("missing discovery accepted")
	}
	if err := b.validateConfig(&config_pb.Client{Clients: map[string]*config_pb.ClientOption{"orders": {}}}); err == nil {
		t.Fatal("missing default discovery accepted")
	}
	// 直连目标不依赖服务发现，即使声明了无关发现名称也不创建监听。
	if err := b.validateConfig(&config_pb.Client{Clients: map[string]*config_pb.ClientOption{"orders": {Target: "localhost:9000", Discovery: "missing"}}}); err != nil {
		t.Fatal(err)
	}
	a := newClientSpec("orders", &config_pb.ClientOption{Discovery: "a"}, nil)
	other := newClientSpec("orders", &config_pb.ClientOption{Discovery: "b"}, nil)
	if a.equal(other) {
		t.Fatal("discovery changes must replace cached connections")
	}
	first := &staticDiscovery{}
	second := &staticDiscovery{}
	b.discoveries = testResolver{"default": first, "a": first, "b": second}
	for _, item := range []struct {
		name string
		want registry.Discovery
	}{{"", first}, {"default", first}, {"a", first}, {"b", second}} {
		got, err := b.resolveDiscovery(newClientSpec("orders", &config_pb.ClientOption{Discovery: item.name}, nil))
		if err != nil || got != item.want {
			t.Fatalf("selection %s: %v", item.name, err)
		}
	}
	manager := testconfig.Empty(t)
	factory, cleanup, err := NewFactory(manager, newTestLogger(discardLogger{}), appinfo.New("test"), b.tracing, b.metrics, testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if factory == nil {
		t.Fatal("nil factory")
	}
}
