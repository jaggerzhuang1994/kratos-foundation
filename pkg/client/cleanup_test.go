package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type blockingLogState struct {
	enteredOnce sync.Once
	entered     chan struct{}
	unblock     chan struct{}
}

type blockingLogger struct {
	state *blockingLogState
}

func (l *blockingLogger) Log(kratoslog.Level, ...any) error {
	l.state.enteredOnce.Do(func() { close(l.state.entered) })
	<-l.state.unblock
	return nil
}

func TestFactoryCleanupWaitsForObserverRejectionLog(t *testing.T) {
	manager := &capturedObserverManager{
		initial:      new(config_pb.Client),
		cancelSignal: make(chan struct{}),
	}
	state := &blockingLogState{
		entered: make(chan struct{}),
		unblock: make(chan struct{}),
	}
	unblockLogger := idempotentClose(state.unblock)
	logger := newTestLogger(&blockingLogger{state: state})
	clientFactory, cleanup, err := newFactory(manager, newFakeBuilder(t), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	t.Cleanup(unblockLogger)
	f := clientFactory.(*factory)

	callbackDone := make(chan struct{})
	go func() {
		manager.notify("not a client config", nil)
		close(callbackDone)
	}()
	receiveWithin(t, state.entered, "observer rejection log to start")

	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()
	receiveWithin(t, manager.cancelSignal, "config subscription cancellation")
	waitForFactoryClosed(t, f)

	premature := false
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-cleanupDone:
		premature = true
	case <-timer.C:
	}

	unblockLogger()
	receiveWithin(t, callbackDone, "observer rejection callback")
	receiveWithin(t, cleanupDone, "cleanup after observer rejection log")
	if premature {
		t.Fatal("cleanup returned while observer rejection log was blocked")
	}
}

func TestFactoryCleanupWaitsForRelease(t *testing.T) {
	clientFactory, cleanup, closeCount := newReadyFactoryWithCleanup(t, "orders")
	_, _, release, err := clientFactory.AcquireClient(context.Background(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	waitForFactoryClosed(t, clientFactory.(*factory))
	select {
	case <-done:
		t.Fatal("cleanup returned before release")
	default:
	}
	release()
	receiveWithin(t, done, "cleanup after release")
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
}

func TestFactoryCleanupIsIdempotent(t *testing.T) {
	clientFactory, cleanup, closeCount := newReadyFactoryWithCleanup(t, "orders")
	f := clientFactory.(*factory)
	logger, recorder := newRecordingLogger()
	f.logger = logger

	cleanup()
	cleanup()
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
	fields := map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "factory_closed",
	}
	if got := countLogRecords(recorder, "client closed", fields); got != 1 {
		t.Fatalf("matching close log count = %d, want 1", got)
	}
}

func TestFactoryCleanupCancelsBuildAndRejectsAcquire(t *testing.T) {
	started := make(chan struct{})
	builder := &fakeBuilder{buildFn: func(ctx context.Context, _ clientSpec) (clientResult, error) {
		close(started)
		<-ctx.Done()
		return clientResult{}, ctx.Err()
	}}
	clientFactory, cleanup := newFactoryWithoutSubscription(t, builder)
	acquireErr := make(chan error, 1)
	go func() {
		_, _, _, err := clientFactory.AcquireClient(context.Background(), "orders")
		acquireErr <- err
	}()
	receiveWithin(t, started, "build to start")
	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()
	receiveWithin(t, cleanupDone, "cleanup after build cancellation")
	if err := receiveWithin(t, acquireErr, "in-flight acquisition to fail"); !errors.Is(err, ErrFactoryClosed) {
		t.Fatalf("in-flight error = %v, want %v", err, ErrFactoryClosed)
	}
	if _, _, _, err := clientFactory.AcquireClient(context.Background(), "orders"); !errors.Is(err, ErrFactoryClosed) {
		t.Fatalf("post-close error = %v, want %v", err, ErrFactoryClosed)
	}
}

func TestFactoryCleanupWaitsForEnteredConfigUpdate(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	unblockUpdate := idempotentClose(unblock)
	t.Cleanup(unblockUpdate)
	var block atomic.Bool
	realBuilder := newTestRealBuilder(t, nil)
	builder := &fakeBuilder{
		validateFn: func(config *config_pb.Client) error {
			if block.Load() {
				close(started)
				<-unblock
			}
			return realBuilder.validateConfig(config)
		},
		buildFn: func(context.Context, clientSpec) (clientResult, error) {
			return clientResult{}, errors.New("unexpected build")
		},
	}
	clientFactory, cleanup := newFactoryWithoutSubscription(t, builder)
	block.Store(true)
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- updateFactoryConfig(clientFactory, configWithTarget("orders", "http://127.0.0.1:2"))
	}()
	receiveWithin(t, started, "config validation to start")
	cleanupDone := make(chan struct{})
	go func() { cleanup(); close(cleanupDone) }()
	waitForFactoryClosed(t, clientFactory)
	select {
	case <-cleanupDone:
		t.Fatal("cleanup returned before config validation exited")
	default:
	}
	unblockUpdate()
	if err := receiveWithin(t, updateDone, "config update to exit"); !errors.Is(err, ErrFactoryClosed) {
		t.Fatalf("update error = %v, want %v", err, ErrFactoryClosed)
	}
	receiveWithin(t, cleanupDone, "cleanup after config update")
}

func TestFactoryCleanupWaitsForLateSuccessfulBuild(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	openGate := idempotentClose(gate)
	t.Cleanup(openGate)
	var closes atomic.Int32
	builder := &fakeBuilder{buildFn: func(_ context.Context, spec clientSpec) (clientResult, error) {
		close(started)
		<-gate
		return fakeResultForSpec(spec, &closes), nil
	}}
	f, cleanup := newFactoryWithoutSubscription(t, builder)
	logger, recorder := newRecordingLogger()
	f.logger = logger
	acquired := acquireClientAsync(f, context.Background(), "orders")
	receiveWithin(t, started, "cancel-ignoring build to start")

	cleanupDone := make(chan struct{})
	go func() { cleanup(); close(cleanupDone) }()
	waitForFactoryClosed(t, f)
	select {
	case <-cleanupDone:
		t.Fatal("cleanup returned before delayed builder")
	default:
	}
	openGate()
	result := receiveWithin(t, acquired, "late-build acquisition result")
	if result.httpClient != nil || result.grpcClient != nil || result.release != nil {
		t.Fatal("closed factory published late build result")
	}
	if !errors.Is(result.err, ErrFactoryClosed) {
		t.Fatalf("acquisition error = %v, want %v", result.err, ErrFactoryClosed)
	}
	receiveWithin(t, cleanupDone, "cleanup after delayed builder")
	if got := closes.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
	recorder.requireRecord(t, "client closed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "GRPC",
		"reason":   "factory_closed",
	})
	if _, _, _, err := f.AcquireClient(context.Background(), "orders"); !errors.Is(err, ErrFactoryClosed) {
		t.Fatalf("post-cleanup acquisition error = %v, want %v", err, ErrFactoryClosed)
	}
}

func TestFactoryCleanupPreservesStaleBuildReason(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	openGate := idempotentClose(gate)
	t.Cleanup(openGate)
	var closes atomic.Int32
	f := newConfiguredTestFactory(
		t,
		configWithTarget("orders", "http://127.0.0.1:1"),
		func(context.Context, clientSpec) (clientResult, error) {
			close(started)
			<-gate
			return fakeHTTPResult(&closes), nil
		},
	)
	logger, recorder := newRecordingLogger()
	f.logger = logger
	cleanup := f.cleanup(func() {})
	acquired := acquireClientAsync(f, context.Background(), "orders")
	receiveWithin(t, started, "eventually-stale build to start")
	if err := updateFactoryConfig(f, configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
		t.Fatal(err)
	}

	cleanupDone := make(chan struct{})
	go func() { cleanup(); close(cleanupDone) }()
	waitForFactoryClosed(t, f)
	openGate()
	result := receiveWithin(t, acquired, "stale-build acquisition result")
	if !errors.Is(result.err, ErrFactoryClosed) {
		t.Fatalf("acquisition error = %v, want %v", result.err, ErrFactoryClosed)
	}
	receiveWithin(t, cleanupDone, "cleanup after stale builder")
	if got := closes.Load(); got != 1 {
		t.Fatalf("close count = %d, want 1", got)
	}
	recorder.requireRecord(t, "client closed", map[string]any{
		"client":   "orders",
		"revision": uint64(1),
		"protocol": "HTTP",
		"reason":   "stale_build",
	})
	if got := countLogRecords(recorder, "client closed", map[string]any{
		"reason": "factory_closed",
	}); got != 0 {
		t.Fatalf("factory-closed log count for stale build = %d, want 0", got)
	}
}

func countLogRecords(recorder *logRecorder, message string, fields map[string]any) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	count := 0
	for _, record := range recorder.records {
		values := make(map[string]any, len(record.keyvals)/2)
		for index := 0; index+1 < len(record.keyvals); index += 2 {
			key, ok := record.keyvals[index].(string)
			if ok {
				values[key] = record.keyvals[index+1]
			}
		}
		if values[kratoslog.DefaultMessageKey] != message {
			continue
		}
		matched := true
		for key, want := range fields {
			got := values[key]
			if errorWant, ok := want.(error); ok {
				gotError, ok := got.(error)
				if !ok || !errors.Is(gotError, errorWant) {
					matched = false
					break
				}
				continue
			}
			if got != want {
				matched = false
				break
			}
		}
		if matched {
			count++
		}
	}
	return count
}

func recordedLogErrors(recorder *logRecorder, message string) []error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var result []error
	for _, record := range recorder.records {
		values := make(map[string]any, len(record.keyvals)/2)
		for index := 0; index+1 < len(record.keyvals); index += 2 {
			key, ok := record.keyvals[index].(string)
			if ok {
				values[key] = record.keyvals[index+1]
			}
		}
		if values[kratoslog.DefaultMessageKey] != message {
			continue
		}
		recorded, _ := values["error"].(error)
		result = append(result, recorded)
	}
	return result
}

func TestCleanupBoundsLeakedCurrentAndRetiredLeases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, cleanup, closes := newReadyFactoryWithCleanup(t, "orders")
		_, _, releaseOld, err := f.AcquireClient(context.Background(), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer releaseOld()
		if err := updateFactoryConfig(f.(*factory), configWithTarget("orders", "http://127.0.0.1:2")); err != nil {
			t.Fatal(err)
		}
		_, _, releaseNew, err := f.AcquireClient(context.Background(), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer releaseNew()
		done := make(chan struct{})
		go func() { cleanup(); close(done) }()
		time.Sleep(31 * time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Error("cleanup still waits for leaked leases after its budget")
			releaseOld()
			releaseNew()
			<-done
			return
		}
		if closes.Load() != 2 {
			t.Fatalf("closed %d clients, want both versions", closes.Load())
		}
		releaseOld()
		releaseNew()
		cleanup()
		if closes.Load() != 2 {
			t.Fatal("late releases closed clients twice")
		}
	})
}

func TestCleanupUsesStartupBudgetAndReportsForcedClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		initial := configWithTarget("orders", "http://127.0.0.1:1")
		initial.CleanupTimeout = durationpb.New(time.Second)
		var closes atomic.Int32
		logger, recorder := newRecordingLogger()
		clientFactory, cleanup, err := newFactory(&capturedObserverManager{initial: initial}, &fakeBuilder{buildFn: func(context.Context, clientSpec) (clientResult, error) { return fakeHTTPResult(&closes), nil }}, logger)
		if err != nil {
			t.Fatal(err)
		}
		_, _, release, err := clientFactory.AcquireClient(context.Background(), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		next := proto.CloneOf(initial)
		next.CleanupTimeout = durationpb.New(time.Minute)
		if err := updateFactoryConfig(clientFactory.(*factory), next); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { cleanup(); close(done) }()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			release()
			<-done
			t.Fatal("hot update changed startup shutdown budget")
		}
		if closes.Load() != 1 {
			t.Fatalf("closes=%d", closes.Load())
		}
		if !recorder.hasRecord("factory.cleanup | timeout | force closing clients", map[string]any{"connections": 1, "timeout": time.Second}) {
			t.Fatal("missing forced-close diagnostic")
		}
		release()
		release()
		if closes.Load() != 1 {
			t.Fatal("late release closed the client twice")
		}
	})
}

func TestFactoryRejectsInvalidCleanupBudget(t *testing.T) {
	for _, duration := range []*durationpb.Duration{durationpb.New(0), durationpb.New(-time.Second), {Nanos: 1_000_000_000}, {Seconds: 10_000_000_000}} {
		initial := &config_pb.Client{CleanupTimeout: duration}
		_, cleanup, err := newFactory(&capturedObserverManager{initial: initial}, newFakeBuilder(t), newTestLogger(discardLogger{}))
		if cleanup != nil {
			cleanup()
		}
		if err == nil {
			t.Fatalf("accepted cleanup timeout %v", duration)
		}
	}
}
