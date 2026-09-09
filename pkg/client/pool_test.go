package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	stdgrpc "google.golang.org/grpc"
)

type gatedDoneContext struct {
	context.Context
	once    sync.Once
	entered chan<- struct{}
	unblock <-chan struct{}
}

func (c *gatedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { c.entered <- struct{}{} })
	<-c.unblock
	return c.Context.Done()
}

func TestFactoryBuildsDifferentNamesConcurrently(t *testing.T) {
	t.Parallel()
	entered := make(chan string, 2)
	unblock := make(chan struct{})
	unblockBuilds := idempotentClose(unblock)
	t.Cleanup(unblockBuilds)
	errs := make(chan error, 2)
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(_ context.Context, spec clientSpec) (clientResult, error) {
		entered <- spec.name
		<-unblock
		return fakeHTTPResult(new(atomic.Int32)), nil
	}})
	for _, name := range []string{"orders", "payments"} {
		name := name
		go func() {
			_, _, release, err := factory.AcquireClient(context.Background(), name)
			if err == nil {
				release()
			}
			errs <- err
		}()
	}
	seen := map[string]bool{
		receiveWithin(t, entered, "first named build to enter"):  true,
		receiveWithin(t, entered, "second named build to enter"): true,
	}
	unblockBuilds()
	if !seen["orders"] || !seen["payments"] {
		t.Fatalf("entered names = %v", seen)
	}
	for range 2 {
		if err := receiveWithin(t, errs, "named acquisition result"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFactorySharesBuildFailureWithWaiters(t *testing.T) {
	t.Parallel()
	buildErr := errors.New("dial failed")
	started := make(chan struct{})
	unblock := make(chan struct{})
	unblockBuild := idempotentClose(unblock)
	t.Cleanup(unblockBuild)
	var builds atomic.Int32
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(context.Context, clientSpec) (clientResult, error) {
		if builds.Add(1) == 1 {
			close(started)
		}
		<-unblock
		return clientResult{}, buildErr
	}})
	const callers = 8
	errs := make(chan error, callers)
	waiting := make(chan struct{}, callers)
	for range callers {
		go func() {
			ctx := &observedDoneContext{Context: context.Background(), observed: waiting}
			_, _, _, err := factory.AcquireClient(ctx, "orders")
			errs <- err
		}()
	}
	receiveWithin(t, started, "shared failing build to start")
	for range callers {
		receiveWithin(t, waiting, "failing acquirer to begin waiting")
	}
	unblockBuild()
	var sharedErr error
	for range callers {
		err := receiveWithin(t, errs, "shared build failure")
		if !errors.Is(err, buildErr) {
			t.Fatalf("error = %v, want %v", err, buildErr)
		}
		if sharedErr == nil {
			sharedErr = err
			if unwrapped := errors.Unwrap(sharedErr); unwrapped != buildErr {
				t.Fatalf("unwrapped shared error = %v, want build error directly", unwrapped)
			}
			continue
		}
		if err != sharedErr {
			t.Fatalf("error object differs: got %v, want shared %v", err, sharedErr)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("build count = %d, want 1", got)
	}
	retry := receiveWithin(
		t,
		acquireClientAsync(factory, context.Background(), "orders"),
		"retry after shared build failure",
	)
	if !errors.Is(retry.err, buildErr) {
		t.Fatalf("retry error = %v, want %v", retry.err, buildErr)
	}
	if got := builds.Load(); got != 2 {
		t.Fatalf("build count after retry = %d, want 2", got)
	}
}

func TestFactoryCanceledWaiterDoesNotCancelSharedBuild(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	unblock := make(chan struct{})
	unblockBuild := idempotentClose(unblock)
	t.Cleanup(unblockBuild)
	var builds atomic.Int32
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(ctx context.Context, _ clientSpec) (clientResult, error) {
		if builds.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return clientResult{}, fmt.Errorf("shared build canceled: %w", ctx.Err())
		case <-unblock:
			return fakeHTTPResult(new(atomic.Int32)), nil
		}
	}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	firstErr := make(chan error, 1)
	go func() {
		_, _, _, err := factory.AcquireClient(ctx, "orders")
		firstErr <- err
	}()
	receiveWithin(t, started, "shared build to start")
	cancel()
	if err := receiveWithin(t, firstErr, "canceled waiter result"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v, want context canceled", err)
	}
	waiting := make(chan struct{}, 1)
	second := make(chan acquiredResult, 1)
	go func() {
		secondCtx := &observedDoneContext{Context: context.Background(), observed: waiting}
		httpClient, _, release, err := factory.AcquireClient(secondCtx, "orders")
		second <- acquiredResult{httpClient: httpClient, release: release, err: err}
	}()
	receiveWithin(t, waiting, "second acquirer to begin waiting")
	unblockBuild()
	result := receiveWithin(t, second, "second acquisition result")
	if result.err != nil || result.httpClient == nil {
		t.Fatalf("second acquisition: client=%v error=%v", result.httpClient, result.err)
	}
	result.release()
	if got := builds.Load(); got != 1 {
		t.Fatalf("build count = %d, want 1", got)
	}
}

func TestFactoryCompletedBuildRechecksState(t *testing.T) {
	t.Parallel()

	t.Run("stale error is hidden by cached current version", func(t *testing.T) {
		buildErr := errors.New("old build failed")
		started := make(chan struct{})
		unblockBuild := make(chan struct{})
		unblockBuilder := idempotentClose(unblockBuild)
		t.Cleanup(unblockBuilder)
		selectEntered := make(chan struct{}, 1)
		unblockSelect := make(chan struct{})
		unblockWaiter := idempotentClose(unblockSelect)
		t.Cleanup(unblockWaiter)
		factory := newConfiguredTestFactory(
			t,
			configWithTarget("orders", "http://old-orders.test"),
			func(context.Context, clientSpec) (clientResult, error) {
				close(started)
				<-unblockBuild
				return clientResult{}, buildErr
			},
		)
		ctx := &gatedDoneContext{
			Context: context.Background(),
			entered: selectEntered,
			unblock: unblockSelect,
		}
		acquired := acquireClientAsync(factory, ctx, "orders")

		receiveWithin(t, started, "old build to start")
		receiveWithin(t, selectEntered, "waiter to enter gated select evaluation")
		factory.mu.Lock()
		call := factory.slots["orders"].build
		factory.mu.Unlock()
		if call == nil {
			t.Fatal("missing captured build call")
		}
		unblockBuilder()
		receiveWithin(t, call.done, "old build call to complete")

		replacementClient := new(kratoshttp.Client)
		factory.mu.Lock()
		factory.slots["orders"].current = &clientVersion{
			revision: 2,
			spec:     newClientSpec("orders", configWithTarget("orders", "http://new-orders.test").GetClients()["orders"]),
			client: clientResult{
				httpClient: replacementClient,
			},
		}
		factory.mu.Unlock()
		unblockWaiter()

		result := receiveWithin(t, acquired, "stale-error waiter result")
		if result.err != nil {
			t.Fatalf("acquisition error = %v, want cached current client", result.err)
		}
		if result.httpClient != replacementClient || result.grpcClient != nil || result.release == nil {
			t.Fatalf("acquisition returned HTTP current=%t gRPC=%v release non-nil=%t", result.httpClient == replacementClient, result.grpcClient != nil, result.release != nil)
		}
		result.release()
	})

	t.Run("closed factory wins over completed build error", func(t *testing.T) {
		buildErr := errors.New("build failed before close")
		started := make(chan struct{})
		unblockBuild := make(chan struct{})
		unblockBuilder := idempotentClose(unblockBuild)
		t.Cleanup(unblockBuilder)
		selectEntered := make(chan struct{}, 1)
		unblockSelect := make(chan struct{})
		unblockWaiter := idempotentClose(unblockSelect)
		t.Cleanup(unblockWaiter)
		factory := newConfiguredTestFactory(
			t,
			configWithTarget("orders", "http://orders.test"),
			func(context.Context, clientSpec) (clientResult, error) {
				close(started)
				<-unblockBuild
				return clientResult{}, buildErr
			},
		)
		ctx := &gatedDoneContext{
			Context: context.Background(),
			entered: selectEntered,
			unblock: unblockSelect,
		}
		acquired := acquireClientAsync(factory, ctx, "orders")

		receiveWithin(t, started, "build before close to start")
		receiveWithin(t, selectEntered, "waiter to enter gated select evaluation")
		factory.mu.Lock()
		call := factory.slots["orders"].build
		factory.mu.Unlock()
		if call == nil {
			t.Fatal("missing captured build call")
		}
		unblockBuilder()
		receiveWithin(t, call.done, "build call before close to complete")

		factory.mu.Lock()
		factory.closed = true
		factory.drained = make(chan struct{})
		factory.mu.Unlock()
		unblockWaiter()

		result := receiveWithin(t, acquired, "closed-factory waiter result")
		if result.httpClient != nil || result.grpcClient != nil || result.release != nil {
			t.Fatalf("failed acquisition returned HTTP=%v gRPC=%v release non-nil=%t", result.httpClient != nil, result.grpcClient != nil, result.release != nil)
		}
		if !errors.Is(result.err, ErrFactoryClosed) {
			t.Fatalf("error = %v, want %v", result.err, ErrFactoryClosed)
		}
	})
}

func TestFactoryConcurrentColdAcquireBuildsOnce(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	unblock := make(chan struct{})
	unblockBuild := idempotentClose(unblock)
	t.Cleanup(unblockBuild)
	var builds atomic.Int32
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(context.Context, clientSpec) (clientResult, error) {
		if builds.Add(1) == 1 {
			close(started)
		}
		<-unblock
		return fakeHTTPResult(new(atomic.Int32)), nil
	}})
	const callers = 32
	results := make(chan acquiredResult, callers)
	waiting := make(chan struct{}, callers)
	for range callers {
		go func() {
			ctx := &observedDoneContext{Context: context.Background(), observed: waiting}
			httpClient, grpcClient, release, err := factory.AcquireClient(ctx, "orders")
			results <- acquiredResult{
				httpClient: httpClient,
				grpcClient: grpcClient,
				release:    release,
				err:        err,
			}
		}()
	}
	receiveWithin(t, started, "cold build to start")
	for range callers {
		receiveWithin(t, waiting, "cold acquirer to begin waiting")
	}
	unblockBuild()
	var first *kratoshttp.Client
	for range callers {
		result := receiveWithin(t, results, "cold acquisition result")
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.httpClient == nil || result.grpcClient != nil || result.release == nil {
			t.Fatalf("cold acquisition returned HTTP=%v gRPC=%v release non-nil=%t", result.httpClient != nil, result.grpcClient != nil, result.release != nil)
		}
		if first == nil {
			first = result.httpClient
		} else if result.httpClient != first {
			t.Fatal("cold acquisitions returned different clients")
		}
		result.release()
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("build count = %d, want 1", got)
	}
}

func TestFactoryAcquireWrapsBuildErrorAndClosesResult(t *testing.T) {
	t.Parallel()
	buildErr := errors.New("sentinel build failure")
	var closes atomic.Int32
	factory := newConfiguredTestFactory(
		t,
		configWithTarget("orders", "http://orders.test"),
		func(context.Context, clientSpec) (clientResult, error) {
			return fakeHTTPResult(&closes), buildErr
		},
	)

	result := receiveWithin(
		t,
		acquireClientAsync(factory, context.Background(), "orders"),
		"build failure acquisition",
	)
	if result.httpClient != nil || result.grpcClient != nil || result.release != nil {
		t.Fatalf("failed acquisition returned HTTP=%v gRPC=%v release non-nil=%t", result.httpClient != nil, result.grpcClient != nil, result.release != nil)
	}
	if !errors.Is(result.err, buildErr) {
		t.Fatalf("error = %v, want errors.Is sentinel", result.err)
	}
	if unwrapped := errors.Unwrap(result.err); unwrapped != buildErr {
		t.Fatalf("unwrapped error = %v, want sentinel directly", unwrapped)
	}
	const contextText = `build client "orders" revision 1 protocol HTTP target "http://orders.test"`
	if !strings.Contains(result.err.Error(), contextText) {
		t.Fatalf("error = %q, want context %q", result.err, contextText)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
}

func TestFactoryAcquireRejectsInvalidBuildResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result func(*atomic.Int32) clientResult
	}{
		{
			name: "both protocol clients",
			result: func(closes *atomic.Int32) clientResult {
				return clientResult{
					httpClient: new(kratoshttp.Client),
					grpcClient: new(stdgrpc.ClientConn),
					closeFn: func() error {
						closes.Add(1)
						return nil
					},
				}
			},
		},
		{
			name: "no protocol client with close function",
			result: func(closes *atomic.Int32) clientResult {
				return clientResult{
					closeFn: func() error {
						closes.Add(1)
						return nil
					},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var closes atomic.Int32
			factory := newConfiguredTestFactory(
				t,
				configWithTarget("orders", "http://orders.test"),
				func(context.Context, clientSpec) (clientResult, error) {
					return test.result(&closes), nil
				},
			)

			result := receiveWithin(
				t,
				acquireClientAsync(factory, context.Background(), "orders"),
				"invalid build result acquisition",
			)
			if result.httpClient != nil || result.grpcClient != nil || result.release != nil {
				t.Fatalf("failed acquisition returned HTTP=%v gRPC=%v release non-nil=%t", result.httpClient != nil, result.grpcClient != nil, result.release != nil)
			}
			if !errors.Is(result.err, ErrInvalidBuildResult) {
				t.Fatalf("error = %v, want %v", result.err, ErrInvalidBuildResult)
			}
			if got := closes.Load(); got != 1 {
				t.Fatalf("close count = %d, want 1", got)
			}
		})
	}
}

func TestFactoryRunBuildClosesStateRejectedResult(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	unblock := make(chan struct{})
	unblockBuild := idempotentClose(unblock)
	t.Cleanup(unblockBuild)
	resultClosed := make(chan struct{}, 2)
	var closes atomic.Int32
	var factory *factory
	logger, recorder := newRecordingLogger()
	factory = newConfiguredTestFactory(
		t,
		configWithTarget("orders", "http://orders.test"),
		func(context.Context, clientSpec) (clientResult, error) {
			close(started)
			<-unblock
			return clientResult{
				httpClient: new(kratoshttp.Client),
				closeFn: func() error {
					// 获取状态锁会让锁内 close 的错误实现确定性死锁并触发等待上界。
					factory.mu.Lock()
					closed := factory.closed
					factory.mu.Unlock()
					if !closed {
						t.Error("state-rejected result closed before factory was marked closed")
					}
					closes.Add(1)
					resultClosed <- struct{}{}
					return nil
				},
			}, nil
		},
	)
	factory.logger = logger

	acquired := acquireClientAsync(factory, context.Background(), "orders")
	receiveWithin(t, started, "state-rejected build to start")
	factory.mu.Lock()
	factory.closed = true
	factory.drained = make(chan struct{})
	factory.mu.Unlock()
	unblockBuild()

	result := receiveWithin(t, acquired, "state-rejected acquisition result")
	if result.httpClient != nil || result.grpcClient != nil || result.release != nil {
		t.Fatalf("failed acquisition returned HTTP=%v gRPC=%v release non-nil=%t", result.httpClient != nil, result.grpcClient != nil, result.release != nil)
	}
	if !errors.Is(result.err, ErrFactoryClosed) {
		t.Fatalf("error = %v, want %v", result.err, ErrFactoryClosed)
	}
	receiveWithin(t, resultClosed, "state-rejected result close")
	if got := closes.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
	recorder.requireRecord(t, "client closed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "stale_build",
	})
}

func TestFactoryReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	factory := newReadyTestFactory(t, "orders")
	_, _, release, err := factory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if refs := currentReferences(factory, "orders"); refs != 0 {
		t.Fatalf("references = %d, want 0", refs)
	}
}

func TestFactoryAcquireRejectsEmptyName(t *testing.T) {
	t.Parallel()
	var builds atomic.Int32
	factory := newTestFactory(t, &fakeBuilder{buildFn: func(context.Context, clientSpec) (clientResult, error) {
		builds.Add(1)
		return clientResult{}, errors.New("unexpected build")
	}})
	if _, _, release, err := factory.AcquireClient(context.Background(), ""); !errors.Is(err, ErrInvalidClientName) || release != nil {
		t.Fatalf("empty name: release non-nil=%t error=%v", release != nil, err)
	}
	if got := builds.Load(); got != 0 {
		t.Fatalf("build count = %d, want 0", got)
	}
}

func TestFactoryAcquirePanicsOnNilContext(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("AcquireClient accepted a nil context")
		}
	}()

	factory := newTestFactory(t, newFakeBuilder(t))
	_, _, _, _ = factory.AcquireClient(nil, "orders")
}

func TestClientResultRejectsBothProtocols(t *testing.T) {
	t.Parallel()
	result := clientResult{httpClient: new(kratoshttp.Client), grpcClient: new(stdgrpc.ClientConn)}
	if !errors.Is(result.validate(), ErrInvalidBuildResult) {
		t.Fatalf("error = %v, want %v", result.validate(), ErrInvalidBuildResult)
	}
}
