package server

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/errtree"
)

var errListenerNotPrepared = errors.New("http listener is not prepared")

// managedListener 把监听所有权留在 Foundation，构造时不绑定地址，首次 prepare 时才 Listen。
//
// mu 串行化准备、发布与关闭。Listen 在锁内执行：本地 bind 不阻塞，地址解析受调用方 Context
// 和 abort 约束；close 先取消 abort 再加锁，因此不会被进行中的 Listen 长时间阻塞。
// Accept 只在锁内读取状态，阻塞的底层 Accept 在锁外执行。
type managedListener struct {
	network string
	address string
	// abort 在关闭时取消，用于中断进行中的 Listen。
	abort  context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	listener   net.Listener
	prepared   bool
	prepareErr error
	closed     bool
	closeErr   error
}

func newManagedListener(network, address string, supplied net.Listener) *managedListener {
	abort, cancel := context.WithCancel(context.Background())
	return &managedListener{network: network, address: address, listener: supplied, abort: abort, cancel: cancel}
}

// prepare 绑定并发布监听；成功或失败结果均被共享，失败不重试。
// 调用方 Context 已取消时返回其错误且不记录结果；关闭开始后返回 net.ErrClosed。
func (l *managedListener) prepare(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.abort.Err() != nil {
		return net.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.prepared || l.prepareErr != nil {
		return l.prepareErr
	}
	if l.listener == nil {
		listenCtx, stop := context.WithCancel(ctx)
		defer stop()
		unwatch := context.AfterFunc(l.abort, stop)
		defer unwatch()
		var config net.ListenConfig
		listener, err := config.Listen(listenCtx, l.network, l.address)
		// 数字地址的本地 bind 不一定观察 Context；发布前再次确认取消状态并回收未发布的监听。
		if err == nil {
			if err = listenCtx.Err(); err != nil {
				err = errors.Join(err, closeListener(listener))
			}
		}
		if err != nil {
			if l.abort.Err() != nil {
				// 关闭打断了准备，按“已关闭”而非启动故障返回，Start 据此正常退出。
				err = net.ErrClosed
			}
			l.prepareErr = err
			return err
		}
		l.listener = listener
	}
	l.prepared = true
	return nil
}

func (l *managedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	closed, prepared, listener := l.closed, l.prepared, l.listener
	l.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if !prepared {
		return nil, errListenerNotPrepared
	}
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

// Close 永久关闭监听，并释放调用方转交或已绑定的底层 Listener；重复调用共享第一次的结果。
func (l *managedListener) Close() error {
	l.cancel()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	l.closed = true
	// 本地 fd 关闭不阻塞，在锁内执行可保证与准备、发布互斥，不会遗漏晚到的监听。
	l.closeErr = closeListener(l.listener)
	return l.closeErr
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	// 仅归一化纯关闭错误，避免吞掉合并错误中的真实回收失败。
	if errtree.Only(err, net.ErrClosed) {
		return nil
	}
	return err
}

type unpreparedListenerAddr struct {
	network string
	address string
}

func (a unpreparedListenerAddr) Network() string { return a.network }
func (a unpreparedListenerAddr) String() string  { return a.address }
