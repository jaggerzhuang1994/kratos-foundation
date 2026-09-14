package subscription

import (
	"runtime/debug"
	"sync"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// Notification 是一次订阅投递。Terminal 表示投递后关闭该订阅。
type Notification struct {
	Key      string
	Value    any
	Found    bool
	Err      error
	Terminal bool
}

// Callback 消费一条订阅通知。
type Callback func(Notification)

// Subscription 为一个调用方提供独立的有界顺序队列。
type Subscription struct {
	mu           sync.Mutex
	callback     Callback
	overloadErr  error
	onStop       func()
	pending      queue
	signal       chan struct{}
	done         chan struct{}
	ready        chan struct{}
	readyOnce    sync.Once
	stopOnce     sync.Once
	accepting    bool
	canceled     bool
	running      bool
	runningSince time.Time
}

// New 创建并启动订阅执行器。
func New(callback Callback, overloadErr error, onStop func()) *Subscription {
	subscription := &Subscription{
		callback:    callback,
		overloadErr: overloadErr,
		onStop:      onStop,
		signal:      make(chan struct{}, 1),
		done:        make(chan struct{}),
		ready:       make(chan struct{}),
		accepting:   true,
	}
	go subscription.run()
	return subscription
}

// Replay 在调用方 goroutine 中同步回放初始值。
func (s *Subscription) Replay(notification Notification) {
	s.mu.Lock()
	canceled := s.canceled
	callback := s.callback
	s.mu.Unlock()
	if !canceled {
		s.notify(callback, notification)
	}
}

// Open 允许执行器在同步回放完成后处理排队更新。
func (s *Subscription) Open() {
	s.readyOnce.Do(func() {
		close(s.ready)
	})
}

// Enqueue 添加通知；返回 true 表示本次写入触发过载终止。
func (s *Subscription) Enqueue(notification Notification) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canceled || !s.accepting {
		return false
	}
	if s.pending.full() {
		s.accepting = false
		s.pending.push(Notification{
			Key:      notification.Key,
			Err:      s.overloadErr,
			Terminal: true,
		})
		s.wake()
		return true
	}
	if notification.Terminal {
		s.accepting = false
	}
	s.pending.push(notification)
	s.wake()
	return false
}

func (s *Subscription) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Subscription) dequeue() (Notification, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending.pop()
}

func (s *Subscription) run() {
	select {
	case <-s.ready:
	case <-s.done:
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-s.signal:
			for {
				notification, ok := s.dequeue()
				if !ok {
					break
				}
				if !s.deliver(notification) {
					return
				}
			}
		}
	}
}

func (s *Subscription) deliver(notification Notification) bool {
	s.mu.Lock()
	if s.canceled {
		s.mu.Unlock()
		return false
	}
	callback := s.callback
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		s.notify(callback, notification)
		close(finished)
	}()
	<-started
	s.mu.Unlock()

	select {
	case <-s.done:
		return false
	case <-finished:
		if notification.Terminal {
			s.Cancel()
			return false
		}
		return true
	}
}

// Cancel 幂等终止订阅，不等待正在执行的业务回调。
func (s *Subscription) Cancel() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.canceled = true
		s.accepting = false
		close(s.done)
		onStop := s.onStop
		s.mu.Unlock()
		s.readyOnce.Do(func() {
			close(s.ready)
		})
		if onStop != nil {
			onStop()
		}
	})
}

// State 是订阅自身同步边界内的只读运行状态。
type State struct {
	Accepting    bool
	Pending      int
	Running      bool
	RunningSince time.Time
}

// Status 即使业务回调阻塞也可读取；不执行任何业务代码。
func (s *Subscription) Status() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{Accepting: s.accepting && !s.canceled, Pending: len(s.pending.items), Running: s.running, RunningSince: s.runningSince}
}

func (s *Subscription) notify(callback Callback, notification Notification) {
	s.mu.Lock()
	s.running = true
	s.runningSince = time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.runningSince = time.Time{}
		s.mu.Unlock()
	}()
	notify(callback, notification)
}

func notify(callback Callback, notification Notification) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.WithModule("config").With(
				"function", "notify",
				"key", notification.Key,
				"panic", recovered,
				"stack", string(debug.Stack()),
			).Error("Configuration observer panicked while processing an update")
		}
	}()
	callback(notification)
}

const queueCapacity = 16

type queue struct {
	items []Notification
}

func (q *queue) full() bool {
	return len(q.items) >= queueCapacity
}

func (q *queue) push(notification Notification) {
	q.items = append(q.items, notification)
}

func (q *queue) pop() (Notification, bool) {
	if len(q.items) == 0 {
		return Notification{}, false
	}
	next := q.items[0]
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil
	}
	return next, true
}
