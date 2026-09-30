package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestManagedListenerBeforePrepare(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		name := "configured"
		if supplied {
			name = "supplied"
		}
		t.Run(name, func(t *testing.T) {
			stub := &managedListenerStub{address: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}}
			var source net.Listener
			if supplied {
				source = stub
			}
			listener := newManagedListener("tcp4", "127.0.0.1:0", source)
			address := listener.Addr()
			if _, ok := address.(*net.TCPAddr); ok {
				t.Fatal("unprepared address is a TCPAddr")
			}
			if address.Network() != "tcp4" || address.String() != "127.0.0.1:0" {
				t.Fatalf("placeholder address = %s %s", address.Network(), address)
			}
			if connection, err := listener.Accept(); connection != nil || err != errListenerNotPrepared {
				t.Fatalf("unprepared Accept = %v, %v", connection, err)
			}
			if stub.addrCalls.Load() != 0 || stub.closeCalls.Load() != 0 {
				t.Fatal("construction or unprepared operations touched supplied listener")
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := listener.prepare(ctx); err != net.ErrClosed {
				t.Fatalf("closed prepare = %v", err)
			}
			if _, err := listener.Accept(); err != net.ErrClosed {
				t.Fatalf("closed Accept = %v", err)
			}
			wantCloses := int32(0)
			if supplied {
				wantCloses = 1
			}
			if got := stub.closeCalls.Load(); got != wantCloses {
				t.Fatalf("supplied close calls = %d, want %d", got, wantCloses)
			}
		})
	}
}

func TestManagedListenerSuppliedOperations(t *testing.T) {
	connection, peer := net.Pipe()
	t.Cleanup(func() { _ = connection.Close(); _ = peer.Close() })
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4321}
	stub := &managedListenerStub{address: address, accept: func() (net.Conn, error) { return connection, nil }}
	listener := newManagedListener("tcp", "ignored:0", stub)
	for range 2 {
		if err := listener.prepare(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if listener.Addr() != address {
		t.Fatal("prepared address does not come from supplied listener")
	}
	if got, err := listener.Accept(); got != connection || err != nil {
		t.Fatalf("prepared Accept = %v, %v", got, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if listener.Addr() != address || stub.closeCalls.Load() != 1 {
		t.Fatal("close lost address or did not release supplied listener once")
	}
}

func TestManagedListenerPrepareFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		network string
		address string
	}{
		{name: "invalid network", network: "invalid-network", address: "127.0.0.1:0"},
		{name: "invalid address", network: "tcp", address: "missing-port"},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener := newManagedListener(test.network, test.address, nil)
			first := listener.prepare(context.Background())
			if first == nil {
				t.Fatal("invalid listen configuration succeeded")
			}
			if err := listener.prepare(context.Background()); err != first {
				t.Fatalf("preparation failure was not shared: first=%v next=%v", first, err)
			}
			if _, ok := listener.Addr().(*net.TCPAddr); ok {
				t.Fatal("failed prepare published a listener")
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if err := listener.prepare(context.Background()); err != net.ErrClosed {
				t.Fatalf("prepare after close = %v", err)
			}
		})
	}
	listener := newManagedListener("tcp", "127.0.0.1:0", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := listener.prepare(ctx); err != context.Canceled {
		t.Fatalf("canceled prepare = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedListenerConcurrentClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("close listener failed")
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		t.Cleanup(unblock)
		stub := &managedListenerStub{close: func() error { close(entered); <-release; return failure }}
		listener := newManagedListener("tcp", "127.0.0.1:0", stub)
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- listener.Close() }()
		<-entered
		go func() { second <- listener.Close() }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := listener.close(ctx); err != context.Canceled {
			t.Fatalf("canceled close wait = %v", err)
		}
		if _, err := listener.Accept(); err != net.ErrClosed {
			t.Fatalf("Accept during close = %v", err)
		}
		unblock()
		if <-first != failure || <-second != failure || listener.close(ctx) != failure {
			t.Fatal("concurrent or repeated close did not share final error")
		}
		if stub.closeCalls.Load() != 1 {
			t.Fatal("underlying listener closed more than once")
		}
	})
	failure := errors.New("release listener failed")
	joinedFailure := errors.Join(net.ErrClosed, failure)
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "closed", err: net.ErrClosed},
		{name: "wrapped closed", err: &net.OpError{Op: "close", Net: "tcp", Err: net.ErrClosed}},
		{name: "joined closed", err: errors.Join(net.ErrClosed, fmt.Errorf("release: %w", net.ErrClosed))},
		{name: "joined failure", err: joinedFailure, want: joinedFailure},
		{name: "ordinary failure", err: failure, want: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &managedListenerStub{close: func() error { return test.err }}
			listener := newManagedListener("tcp", ":0", stub)
			if err := listener.Close(); err != test.want || listener.Close() != test.want {
				t.Fatalf("shared close result = %v, want %v", err, test.want)
			}
			if stub.closeCalls.Load() != 1 {
				t.Fatal("underlying listener closed more than once")
			}
		})
	}
}

func TestManagedListenerCloseDuringPreparation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "unpublished listener"
		if failed {
			name = "failed listen"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				listener := newManagedListener("tcp", "127.0.0.1:0", nil)
				openingCtx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				opening := &listenerOpening{done: make(chan struct{}), cancel: cancel}
				// 在生产发布边界构造正在 Listen 的状态，不为测试引入监听 factory。
				listener.opening = opening
				prepareResult, closeResult := make(chan error, 1), make(chan error, 1)
				go func() { prepareResult <- listener.prepare(context.Background()) }()
				synctest.Wait()
				waitCtx, waitCancel := context.WithCancel(context.Background())
				waitResult := make(chan error, 1)
				go func() { waitResult <- listener.prepare(waitCtx) }()
				synctest.Wait()
				waitCancel()
				if err := <-waitResult; err != context.Canceled {
					t.Fatalf("canceled prepare waiter = %v", err)
				}
				if openingCtx.Err() != nil {
					t.Fatal("prepare waiter canceled shared Listen")
				}
				go func() { closeResult <- listener.Close() }()
				<-openingCtx.Done()
				synctest.Wait()
				var created net.Listener
				var prepareErr, closeErr error
				var stub *managedListenerStub
				if failed {
					prepareErr = context.Canceled
				} else {
					closeErr = errors.New("unpublished close failed")
					stub = &managedListenerStub{close: func() error { return closeErr }}
					created = stub
				}
				if err := listener.finishOpening(opening, created, prepareErr); err != net.ErrClosed {
					t.Fatalf("closed opening result = %v", err)
				}
				prepared, closed := <-prepareResult, <-closeResult
				if prepared != net.ErrClosed || !errors.Is(closed, closeErr) || listener.Close() != closed {
					t.Fatalf("preparation and close results were not shared: prepare=%v close=%v", prepared, closed)
				}
				if stub != nil && stub.closeCalls.Load() != 1 {
					t.Fatal("unpublished listener was not closed once")
				}
				if _, ok := listener.Addr().(*net.TCPAddr); ok {
					t.Fatal("closing preparation published a listener")
				}
			})
		})
	}
}

func TestManagedListenerPreparationRollbackAfterBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := newManagedListener("tcp", "127.0.0.1:0", nil)
		openingCtx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		opening := &listenerOpening{done: make(chan struct{}), cancel: cancel}
		listener.opening = opening
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := listener.close(ctx); err != context.DeadlineExceeded {
			t.Fatalf("close budget result = %v", err)
		}
		if openingCtx.Err() != context.Canceled {
			t.Fatal("close did not cancel pending Listen")
		}
		stub := &managedListenerStub{}
		if err := listener.finishOpening(opening, stub, nil); err != net.ErrClosed {
			t.Fatalf("late prepare result = %v", err)
		}
		if err := listener.Close(); err != nil || stub.closeCalls.Load() != 1 {
			t.Fatalf("late cleanup = %v, closes=%d", err, stub.closeCalls.Load())
		}
	})
}

func TestManagedListenerRejectsCanceledListenResult(t *testing.T) {
	listener := newManagedListener("tcp", "127.0.0.1:0", nil)
	_, cancel := context.WithCancel(context.Background())
	opening := &listenerOpening{done: make(chan struct{}), cancel: cancel}
	listener.opening = opening
	closeErr := errors.New("close canceled bind failed")
	stub := &managedListenerStub{close: func() error { return closeErr }}
	result := listener.finishOpening(opening, stub, context.Canceled)
	if !errors.Is(result, context.Canceled) || !errors.Is(result, closeErr) {
		t.Fatalf("canceled bind result = %v", result)
	}
	if stub.closeCalls.Load() != 1 || listener.prepare(context.Background()) != result {
		t.Fatal("canceled bind was published or preparation error was not retained")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedListenerLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener := newManagedListener("tcp", "127.0.0.1:0", nil)
	t.Cleanup(func() { _ = listener.Close() })
	const callers = 12
	begin, prepared := make(chan struct{}), make(chan error, callers)
	for range callers {
		go func() { <-begin; prepared <- listener.prepare(ctx) }()
	}
	close(begin)
	for range callers {
		if err := <-prepared; err != nil {
			t.Fatal(err)
		}
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port == 0 {
		t.Fatalf("prepared address = %v", listener.Addr())
	}
	if err := listener.prepare(ctx); err != nil || listener.Addr() != address {
		t.Fatalf("repeated prepare changed listener: %v", err)
	}
	accepted := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if connection != nil {
			err = errors.Join(err, connection.Close())
		}
		accepted <- err
	}()
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "tcp", address.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := listener.Accept(); accepted <- err }()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept concurrent with Close = %v", err)
	}
	rebound, err := net.Listen("tcp", address.String())
	if err != nil {
		t.Fatalf("closed address cannot be rebound: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatal(err)
	}
}

type managedListenerStub struct {
	address    net.Addr
	accept     func() (net.Conn, error)
	close      func() error
	addrCalls  atomic.Int32
	closeCalls atomic.Int32
}

func (l *managedListenerStub) Accept() (net.Conn, error) { return l.accept() }
func (l *managedListenerStub) Addr() net.Addr {
	l.addrCalls.Add(1)
	return l.address
}
func (l *managedListenerStub) Close() error {
	l.closeCalls.Add(1)
	if l.close != nil {
		return l.close()
	}
	return nil
}
