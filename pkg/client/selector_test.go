package client

import (
	"context"
	"net/url"
	"testing"

	"github.com/go-kratos/kratos/v2/selector"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
)

func TestMetadataValuesNodeFilterUsesFirstValueAndIgnoresEmptyCandidates(t *testing.T) {
	nodes := []selector.Node{
		testNode{address: "first", metadata: map[string]string{"zone": "hk", "ignored": "anything"}},
		testNode{address: "second", metadata: map[string]string{"zone": "sg"}},
	}
	filter := metadataValuesNodeFilter(url.Values{
		"zone":    {"hk", "sg"},
		"ignored": nil,
	})
	selected := filter(context.Background(), nodes)
	if len(selected) != 1 || selected[0].Address() != "first" {
		t.Fatalf("selected nodes = %#v", selected)
	}

	all := metadataValuesNodeFilter(nil)(context.Background(), nodes)
	if len(all) != len(nodes) || all[0].Address() != "first" || all[1].Address() != "second" {
		t.Fatalf("empty metadata filter changed nodes: %#v", all)
	}
}

// mutableAppInfo 验证调用方返回共享元数据时，Factory 仍持有独立身份快照。
type mutableAppInfo struct {
	appinfo.AppInfo
	metadata map[string]string
}

func (i mutableAppInfo) Metadata() map[string]string { return i.metadata }

func TestFactorySnapshotsAppInfoForDiscoveryRouting(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	info := mutableAppInfo{
		AppInfo: appinfo.New("test"),
		metadata: map[string]string{
			appinfo.MetadataEnvironment: "local",
			appinfo.MetadataHostname:    "injected-host",
		},
	}
	clientFactory, cleanup, err := NewFactory(testconfig.Empty(t), newTestLogger(discardLogger{}), info,
		newTestTracingProvider(t), newTestMetricsProvider(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	info.metadata[appinfo.MetadataEnvironment] = "prod"
	info.metadata[appinfo.MetadataHostname] = "changed-host"
	filters, err := clientFactory.(*factory).builder.(*builder).getNodeFilters(newClientSpec("orders", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	nodes := []selector.Node{
		testNode{scheme: "grpc", address: "injected", metadata: map[string]string{"env": "local", "hostname": "injected-host"}},
		testNode{scheme: "grpc", address: "other-host", metadata: map[string]string{"env": "local", "hostname": "other-host"}},
		testNode{scheme: "grpc", address: "changed", metadata: map[string]string{"env": "prod", "hostname": "changed-host"}},
	}
	for _, filter := range filters {
		nodes = filter(context.Background(), nodes)
	}
	if len(nodes) != 1 || nodes[0].Address() != "injected" {
		t.Fatalf("routing did not preserve injected environment and hostname: %#v", nodes)
	}
}

func TestEnvironmentNodeFilterPreservesLocalFallbacksAndRemoteIsolation(t *testing.T) {
	nodes := []selector.Node{
		testNode{address: "same-host", metadata: map[string]string{"env": "local", "hostname": "host-a"}},
		testNode{address: "other-host", metadata: map[string]string{"env": "local", "hostname": "host-b"}},
		testNode{address: "dev", metadata: map[string]string{"env": "dev", "hostname": "host-a"}},
	}
	for _, test := range []struct {
		name, environment, hostname string
		nodes                       []selector.Node
		want                        []string
	}{
		{"local same host", "local", "host-a", nodes, []string{"same-host"}},
		{"local other host", "local", "missing", nodes, []string{"same-host", "other-host"}},
		{"local other environment", "local", "missing", nodes[2:], []string{"dev"}},
		{"remote matching environment", "dev", "host-b", nodes, []string{"dev"}},
		{"remote no match", "prod", "host-a", nodes, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := environmentNodeFilter(test.environment, test.hostname)(context.Background(), test.nodes)
			if len(selected) != len(test.want) {
				t.Fatalf("selected %#v, want %v", selected, test.want)
			}
			for index, expected := range test.want {
				if selected[index].Address() != expected {
					t.Fatalf("node %d = %q, want %q", index, selected[index].Address(), expected)
				}
			}
		})
	}
}

type testNode struct {
	scheme   string
	address  string
	metadata map[string]string
}

func (n testNode) Scheme() string { return n.scheme }

func (n testNode) Address() string { return n.address }

func (testNode) ServiceName() string { return "orders" }

func (testNode) InitialWeight() *int64 { return nil }

func (testNode) Version() string { return "v1" }

func (n testNode) Metadata() map[string]string { return n.metadata }

func TestProtocolNodeFilters(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"grpc", "http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			nodes := []selector.Node{
				testNode{scheme: scheme, address: "matched"},
				testNode{scheme: "other", address: "ignored"},
			}
			got := schemeNodeFilter(scheme)(context.Background(), nodes)
			if len(got) != 1 || got[0].Address() != "matched" {
				t.Fatalf("selected nodes = %v", got)
			}
		})
	}
}

func TestHTTPSNodeFilterDoesNotMatchHTTP(t *testing.T) {
	t.Parallel()
	nodes := []selector.Node{
		testNode{scheme: schemeHTTPS, address: "https"},
		testNode{scheme: schemeHTTP, address: "http"},
	}
	got := schemeNodeFilter(schemeHTTPS)(context.Background(), nodes)
	if len(got) != 1 || got[0].Address() != "https" {
		t.Fatalf("selected nodes = %v", got)
	}
}

func TestMetadataNodeFilter(t *testing.T) {
	t.Parallel()
	nodes := []selector.Node{
		testNode{address: "hk", metadata: map[string]string{"zone": "hk"}},
		testNode{address: "sg", metadata: map[string]string{"zone": "sg"}},
	}
	got := metadataNodeFilter(map[string]string{"zone": "hk"})(context.Background(), nodes)
	if len(got) != 1 || got[0].Address() != "hk" {
		t.Fatalf("selected nodes = %v", got)
	}
}
