package server

import (
	"context"
	"errors"
	"net"
	"sync"
)

var errListenerNotPrepared = errors.New("http listener is not prepared")

// managedListener 把监听所有权留在 Foundation，构造时不绑定地址。
// mu 只保护准备、发布和关闭状态；外部 Listen、Accept、Addr、Close 均在锁外执行。
type managedListener struct {
	mu         sync.Mutex
	network    string
	address    string
	listener   net.Listener
	prepared   bool
	prepareErr error
	opening    *listenerOpening
	closed     bool
	closeDone  chan struct{}
	closeErr   error
}

type listenerOpening struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

func newManagedListener(network, address string, supplied net.Listener) *managedListener {
	return &managedListener{
		network:   network,
		address:   address,
		listener:  supplied,
		closeDone: make(chan struct{}),
	}
}

// prepare 共享同一次准备结果，失败也不重试；等待者取消不会取消其他调用方的准备。
func (l *managedListener) prepare(ctx context.Context) error {
	contextErr := ctx.Err()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	if contextErr != nil {
		l.mu.Unlock()
		return contextErr
	}
	if l.prepareErr != nil {
		err := l.prepareErr
		l.mu.Unlock()
		return err
	}
	if l.listener != nil {
		l.prepared = true
		l.mu.Unlock()
		return nil
	}
	if opening := l.opening; opening != nil {
		l.mu.Unlock()
		select {
		case <-opening.done:
			return opening.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	listenCtx, cancel := context.WithCancel(ctx)
	opening := &listenerOpening{done: make(chan struct{}), cancel: cancel}
	l.opening = opening
	l.mu.Unlock()

	var config net.ListenConfig
	listener, err := config.Listen(listenCtx, l.network, l.address)
	// 数字地址的本地 bind 不一定观察 Context；发布前再次确认取消状态。
	if err == nil {
		err = listenCtx.Err()
	}
	return l.finishOpening(opening, listener, err)
}

// finishOpening 决定监听能否发布；关闭已开始时由准备调用方回收未发布的资源。
func (l *managedListener) finishOpening(opening *listenerOpening, listener net.Listener, err error) error {
	opening.cancel()
	var closeErr error
	if err != nil && listener != nil {
		closeErr = closeListener(listener)
		listener = nil
		err = errors.Join(err, closeErr)
	}

	l.mu.Lock()
	if !l.closed {
		if err == nil {
			l.listener = listener
			l.prepared = true
		}
		l.prepareErr = err
		l.opening = nil
		opening.err = err
		close(opening.done)
		l.mu.Unlock()
		return err
	}
	l.mu.Unlock()

	// Close 可能已经因预算耗尽返回，但准备调用方仍须完成资源回收。
	closeErr = errors.Join(closeErr, closeListener(listener))
	l.mu.Lock()
	l.closeErr = closeErr
	l.opening = nil
	opening.err = net.ErrClosed
	close(l.closeDone)
	close(opening.done)
	l.mu.Unlock()
	return net.ErrClosed
}

func (l *managedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	if !l.prepared {
		l.mu.Unlock()
		return nil, errListenerNotPrepared
	}
	listener := l.listener
	l.mu.Unlock()
	return listener.Accept()
}

func (l *managedListener) Addr() net.Addr {
	l.mu.Lock()
	listener, prepared := l.listener, l.prepared
	l.mu.Unlock()
	if prepared {
		return listener.Addr()
	}
	// 非 TCPAddr 阻止 Kratos Endpoint 在 prepare 前把占位地址当成真实监听。
	return unpreparedListenerAddr{network: l.network, address: l.address}
}

// Close 永久关闭监听，重复调用共享同一次底层关闭的结果。
func (l *managedListener) Close() error {
	return l.close(context.Background())
}

// close 的预算约束并发准备和关闭的等待；业务 Listener.Close 本身不可被 Context 中断。
func (l *managedListener) close(ctx context.Context) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return l.waitClose(ctx)
	}
	l.closed = true
	opening, listener := l.opening, l.listener
	l.mu.Unlock()
	if opening != nil {
		opening.cancel()
		return l.waitClose(ctx)
	}

	closeErr := closeListener(listener)
	l.mu.Lock()
	l.closeErr = closeErr
	close(l.closeDone)
	l.mu.Unlock()
	return closeErr
}

func (l *managedListener) waitClose(ctx context.Context) error {
	// 已经完成时优先返回共享结果，不让同时取消的 Context 覆盖真实关闭错误。
	select {
	case <-l.closeDone:
		return l.closeErr
	default:
	}
	select {
	case <-l.closeDone:
		return l.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if isListenerClosedOnly(err) {
		return nil
	}
	return err
}

// isListenerClosedOnly 仅归一化纯关闭错误，避免吞掉合并错误中的真实回收失败。
func isListenerClosedOnly(err error) bool {
	switch typed := err.(type) {
	case interface{ Unwrap() []error }:
		causes := typed.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isListenerClosedOnly(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return isListenerClosedOnly(typed.Unwrap())
	default:
		return errors.Is(err, net.ErrClosed)
	}
}

type unpreparedListenerAddr struct {
	network string
	address string
}

func (a unpreparedListenerAddr) Network() string { return a.network }
func (a unpreparedListenerAddr) String() string  { return a.address }
