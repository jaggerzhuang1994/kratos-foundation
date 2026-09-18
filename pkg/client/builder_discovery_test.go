package client

import (
	"context"
	"errors"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type emptyDiscovery struct{}

func (emptyDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (emptyDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	return &emptyWatcher{ctx: ctx, stopped: make(chan struct{})}, nil
}

type emptyWatcher struct {
	ctx       context.Context
	delivered atomic.Bool
	stopOnce  sync.Once
	stopped   chan struct{}
}

func (w *emptyWatcher) Next() ([]*registry.ServiceInstance, error) {
	if w.delivered.CompareAndSwap(false, true) {
		return nil, nil
	}
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *emptyWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return nil
}

func TestBuilderAcceptsInjectedDiscovery(t *testing.T) {
	t.Parallel()
	builder := newTestRealBuilder(t, emptyDiscovery{})
	result, err := builder.build(context.Background(), newClientSpec("orders", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.close() })
	if result.grpcClient == nil || result.httpClient != nil {
		t.Fatal("discovery build did not return exactly one gRPC client")
	}
}

func TestBuilderHTTPDiscoveryDoesNotWaitForInitialNodes(t *testing.T) {
	discovery := newUpdatingHTTPDiscovery()
	builder := newTestRealBuilder(t, discovery)
	protocol := config_pb.Protocol_HTTP
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	result, err := builder.build(ctx, newClientSpec("orders", &config_pb.ClientOption{
		Protocol: &protocol,
		Target:   "discovery:///orders",
	}, nil))
	if err != nil {
		t.Fatalf("build waited for an initial discovery node: %v", err)
	}
	defer func() { _ = result.close() }()
	if result.httpClient == nil || result.grpcClient != nil {
		t.Fatal("discovery build did not return exactly one HTTP client")
	}
	receiveWithin(t, discovery.watchers, "HTTP discovery watcher")
}

type staticDiscovery struct {
	instances []*registry.ServiceInstance
	ready     chan struct{}
}

func (d staticDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return d.instances, nil
}

func (d staticDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	return &staticWatcher{
		ctx:       ctx,
		instances: d.instances,
		ready:     d.ready,
		stopped:   make(chan struct{}),
	}, nil
}

type staticWatcher struct {
	ctx       context.Context
	instances []*registry.ServiceInstance
	delivered atomic.Bool
	ready     chan struct{}
	stopOnce  sync.Once
	stopped   chan struct{}
}

func (w *staticWatcher) Next() ([]*registry.ServiceInstance, error) {
	if w.delivered.CompareAndSwap(false, true) {
		return w.instances, nil
	}
	// resolver 只有消费完首次返回的节点后才会再次调用 Next，此信号用于无 sleep 地同步测试请求。
	if w.ready != nil {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *staticWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return nil
}

type updatingHTTPDiscovery struct {
	updates     chan []*registry.ServiceInstance
	watchers    chan *updatingHTTPWatcher
	nextEntered chan struct{}
	stopErr     error
}

func newUpdatingHTTPDiscovery() *updatingHTTPDiscovery {
	return &updatingHTTPDiscovery{
		updates:     make(chan []*registry.ServiceInstance, 2),
		watchers:    make(chan *updatingHTTPWatcher, 1),
		nextEntered: make(chan struct{}, 4),
	}
}

func (d *updatingHTTPDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (d *updatingHTTPDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	watcher := &updatingHTTPWatcher{
		ctx:         ctx,
		updates:     d.updates,
		nextEntered: d.nextEntered,
		stopped:     make(chan struct{}),
		stopErr:     d.stopErr,
	}
	d.watchers <- watcher
	return watcher, nil
}

type updatingHTTPWatcher struct {
	ctx         context.Context
	updates     <-chan []*registry.ServiceInstance
	nextEntered chan<- struct{}
	stopOnce    sync.Once
	stopped     chan struct{}
	stopErr     error
}

func (w *updatingHTTPWatcher) Next() ([]*registry.ServiceInstance, error) {
	w.nextEntered <- struct{}{}
	select {
	case instances := <-w.updates:
		return instances, nil
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.stopped:
		return nil, context.Canceled
	}
}

func (w *updatingHTTPWatcher) Stop() error {
	w.stopOnce.Do(func() { close(w.stopped) })
	return w.stopErr
}

func TestFactoryHTTPDiscoveryWatcherLivesUntilClientClose(t *testing.T) {
	firstServer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("first"))
	}))
	t.Cleanup(firstServer.Close)
	secondServer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = w.Write([]byte("second"))
	}))
	t.Cleanup(secondServer.Close)

	discovery := newUpdatingHTTPDiscovery()
	discovery.stopErr = errors.New("watcher stop failed")
	instances := func(endpoint string) []*registry.ServiceInstance {
		return []*registry.ServiceInstance{{
			Name:      "orders",
			Endpoints: []string{endpoint},
			Metadata: map[string]string{
				appinfo.MetadataEnvironment: env.AppEnv(),
			},
		}}
	}
	discovery.updates <- instances(firstServer.URL)

	protocol := config_pb.Protocol_HTTP
	initial := &config_pb.Client{Clients: map[string]*config_pb.ClientOption{
		"orders": {
			Protocol: &protocol,
			Target:   "discovery:///orders",
		},
	}}
	builder := newTestRealBuilder(t, discovery)
	clientFactory, cleanup, err := NewFactory(testconfig.New(t, "client", initial), builder.logger, appinfo.New("test"), builder.tracing, builder.metrics, builder.discoveries)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	f := clientFactory.(*factory)

	httpClient, _, release, err := clientFactory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	watcher := receiveWithin(t, discovery.watchers, "HTTP discovery watcher")
	receiveWithin(t, discovery.nextEntered, "initial discovery lookup")
	release()
	f.mu.Lock()
	build := f.slots["orders"].build
	f.mu.Unlock()
	if build != nil {
		receiveWithin(t, build.done, "physical HTTP client build")
	}
	receiveWithin(t, discovery.nextEntered, "post-build discovery watch")
	if err := watcher.ctx.Err(); err != nil {
		t.Fatalf("watch context after physical build = %v, want active", err)
	}

	discovery.updates <- instances(secondServer.URL)
	receiveWithin(t, discovery.nextEntered, "watch after discovery update")

	cachedHTTPClient, _, cachedRelease, err := clientFactory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cachedRelease)
	if cachedHTTPClient != httpClient {
		t.Fatal("discovery update replaced the cached HTTP client")
	}
	requireResponseBody := func(want string) {
		t.Helper()
		requestContext, requestCancel := context.WithTimeout(context.Background(), time.Second)
		defer requestCancel()
		request, requestErr := nethttp.NewRequestWithContext(
			requestContext,
			nethttp.MethodGet,
			"http://orders/value",
			nil,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, requestErr := cachedHTTPClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if got := string(body); got != want {
			t.Fatalf("response body = %q, want %q", got, want)
		}
	}
	requireResponseBody("second")

	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()
	waitForFactoryClosed(t, f)
	if err := watcher.ctx.Err(); err != nil {
		t.Fatalf("watch context while cleanup waits for release = %v, want active", err)
	}

	discovery.updates <- instances(firstServer.URL)
	receiveWithin(t, discovery.nextEntered, "watch during cleanup")
	requireResponseBody("first")

	cachedRelease()
	receiveWithin(t, cleanupDone, "cleanup after cached release")
	receiveWithin(t, watcher.stopped, "HTTP discovery watcher stop")
	receiveWithin(t, watcher.ctx.Done(), "HTTP discovery watch context cancellation")
}
