// Package config 提供配置管理、配置源契约和配置源优先级控制。
package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/decoder"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/snapshot"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/source"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/subscription"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb"
)

type managerWatch struct {
	nextID        uint64
	subscriptions map[uint64]*subscription.Subscription
}

// manager 持有配置源流和最后一份有效的不可变快照。
type manager struct {
	current atomic.Pointer[snapshot.Snapshot]

	// mu 保护生命周期状态和订阅注册表；Source 调用和业务回调必须在释放锁后执行。
	mu         sync.Mutex
	closed     bool
	watchErr   error
	watches    map[string]*managerWatch
	stream     *source.Stream
	streamDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
	status     Status
}

var _ Manager = (*manager)(nil)

func newManager(sources Sources) (*manager, error) {
	values, stream, err := source.Open(sources)
	if err != nil {
		return nil, err
	}
	initial, err := newValidatedSnapshot(values)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("load initial config snapshot: %w", err),
			stream.Close(),
		)
	}
	manager := &manager{
		watches:    make(map[string]*managerWatch),
		stream:     stream,
		streamDone: make(chan struct{}),
	}
	manager.status = Status{Revision: 1, AcceptedUpdates: 1, LastSuccess: time.Now()}
	manager.current.Store(initial)
	go manager.watch()
	return manager, nil
}

func (m *manager) Load(key string, target any, defaultValue ...any) error {
	valueDecoder, err := decoder.New(target, defaultValue)
	if err != nil {
		return fmt.Errorf("load config %q: %w", key, err)
	}
	value, found, err := m.loadValue(key)
	if err != nil {
		return err
	}
	if err := valueDecoder.Apply(value, found, target); err != nil {
		return fmt.Errorf("load config %q: %w", key, err)
	}
	return nil
}

func (m *manager) Subscribe(
	key string,
	prototype any,
	callback Observer,
	defaultValue ...any,
) (func(), error) {
	if callback == nil {
		return nil, errors.New("config observer is nil")
	}
	valueDecoder, err := decoder.New(prototype, defaultValue)
	if err != nil {
		return nil, fmt.Errorf("subscribe config %q: %w", key, err)
	}

	// 在读取回放快照的同一临界区注册，避免更新越过首次回放。
	m.mu.Lock()
	if err := m.stateErrorLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	value, found := m.current.Load().Lookup(key)
	if !found && !valueDecoder.HasDefault() {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	watch := m.watches[key]
	if watch == nil {
		watch = &managerWatch{subscriptions: make(map[uint64]*subscription.Subscription)}
		m.watches[key] = watch
	}
	watch.nextID++
	id := watch.nextID
	valueSubscription := subscription.New(
		func(notification subscription.Notification) {
			defer m.recordCallback(time.Now())
			if notification.Err != nil {
				callback(
					key,
					valueDecoder.NewTarget(),
					fmt.Errorf("observe config %q: %w", key, notification.Err),
				)
				return
			}
			target := valueDecoder.NewTarget()
			loadErr := valueDecoder.Apply(notification.Value, notification.Found, target)
			if loadErr != nil {
				loadErr = fmt.Errorf("load config %q: %w", key, loadErr)
			}
			callback(key, target, loadErr)
		},
		ErrObserverOverloaded,
		func() { m.unregister(key, id) },
	)
	watch.subscriptions[id] = valueSubscription
	m.mu.Unlock()

	valueSubscription.Replay(subscription.Notification{Key: key, Value: value, Found: found})
	valueSubscription.Open()
	return valueSubscription.Cancel, nil
}

func (m *manager) loadValue(key string) (any, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stateErrorLocked(); err != nil {
		return nil, false, err
	}
	value, found := m.current.Load().Lookup(key)
	return value, found, nil
}

func (m *manager) stateErrorLocked() error {
	if m.closed {
		return ErrManagerClosed
	}
	if m.watchErr != nil {
		return m.watchErr
	}
	return nil
}

func (m *manager) unregister(key string, id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	watch := m.watches[key]
	if watch == nil {
		return
	}
	delete(watch.subscriptions, id)
	if len(watch.subscriptions) == 0 {
		delete(m.watches, key)
	}
}

func (m *manager) watch() {
	defer close(m.streamDone)
	for {
		update := m.stream.Next()
		if update.Terminal {
			m.mu.Lock()
			closed := m.closed
			m.mu.Unlock()
			if closed && errors.Is(update.Err, context.Canceled) {
				return
			}
			m.failWatcher(update.Err)
			return
		}
		if update.Err != nil {
			m.recordRejected("source_error")
			m.notifyError(update.Err, false)
			continue
		}
		next, err := newValidatedSnapshot(update.Values)
		if err != nil {
			m.recordRejected("invalid_snapshot")
			log.WithModule("config").With("function", "manager.watch", "error", err).Error("Rejected configuration update; continuing to use the last accepted configuration")
			m.notifyError(fmt.Errorf("load config update snapshot: %w", err), false)
			continue
		}
		m.publish(next)
	}
}

// newValidatedSnapshot 在发布前检查整个 Foundation 配置，避免已删除的顶层字段
// 因为没有组件读取而静默失效；首次加载和热更新共用同一边界。
func newValidatedSnapshot(values []*KeyValue) (*snapshot.Snapshot, error) {
	next, err := snapshot.New(values)
	if err != nil {
		return nil, err
	}
	root, _ := next.Lookup("")
	if err := decoder.ValidateReserved(root, new(kratos_foundation_pb.Config).ProtoReflect().Descriptor(), ""); err != nil {
		return nil, err
	}
	return next, nil
}

type delivery struct {
	subscription *subscription.Subscription
	notification subscription.Notification
}

func (m *manager) publish(next *snapshot.Snapshot) {
	m.mu.Lock()
	if m.closed || m.watchErr != nil {
		m.mu.Unlock()
		return
	}
	previous := m.current.Swap(next)
	m.status.Revision++
	m.status.AcceptedUpdates++
	m.status.LastSuccess = time.Now()
	m.status.LastErrorCode = ""
	deliveries := make([]delivery, 0)
	for key, watch := range m.watches {
		value, found := next.Lookup(key)
		previousValue, previousFound := previous.Lookup(key)
		if previousFound == found && (!found || reflect.DeepEqual(previousValue, value)) {
			continue
		}
		for _, valueSubscription := range watch.subscriptions {
			deliveries = append(deliveries, delivery{
				subscription: valueSubscription,
				notification: subscription.Notification{Key: key, Value: value, Found: found},
			})
		}
	}
	m.mu.Unlock()
	m.deliver(deliveries)
}

func (m *manager) notifyError(err error, terminal bool) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	deliveries := make([]delivery, 0)
	for key, watch := range m.watches {
		for _, valueSubscription := range watch.subscriptions {
			deliveries = append(deliveries, delivery{
				subscription: valueSubscription,
				notification: subscription.Notification{Key: key, Err: err, Terminal: terminal},
			})
		}
	}
	m.mu.Unlock()
	m.deliver(deliveries)
}

func (m *manager) failWatcher(cause error) {
	watchErr := ErrWatcherStopped
	if cause != nil {
		watchErr = fmt.Errorf("%w: %w", ErrWatcherStopped, cause)
	}
	m.mu.Lock()
	if m.closed || m.watchErr != nil {
		m.mu.Unlock()
		return
	}
	m.watchErr = watchErr
	m.status.LastErrorCode = "watcher_stopped"
	deliveries := make([]delivery, 0)
	for key, watch := range m.watches {
		for _, valueSubscription := range watch.subscriptions {
			deliveries = append(deliveries, delivery{
				subscription: valueSubscription,
				notification: subscription.Notification{Key: key, Err: watchErr, Terminal: true},
			})
		}
	}
	m.mu.Unlock()
	log.WithModule("config").With("function", "manager.failWatcher", "error", cause).Error("Configuration watcher stopped; configuration is no longer healthy")
	m.deliver(deliveries)
}

func (m *manager) deliver(deliveries []delivery) {
	for _, next := range deliveries {
		if next.subscription.Enqueue(next.notification) {
			m.mu.Lock()
			m.status.Overloads++
			m.status.LastErrorCode = "observer_overloaded"
			m.mu.Unlock()
			log.WithModule("config").With(
				"function", "manager.deliver",
				"key", next.notification.Key,
				"error", ErrObserverOverloaded,
			).Error("Configuration subscription stopped because its pending update queue is full")
		}
	}
}

// close 幂等关闭 Manager，不等待已经进入业务代码的回调。
func (m *manager) close() error {
	m.closeOnce.Do(func() {
		m.closeErr = m.shutdown()
	})
	return m.closeErr
}

func (m *manager) shutdown() error {
	m.mu.Lock()
	m.closed = true
	subscriptions := make([]*subscription.Subscription, 0)
	for _, watch := range m.watches {
		for _, valueSubscription := range watch.subscriptions {
			subscriptions = append(subscriptions, valueSubscription)
		}
	}
	m.watches = nil
	stream := m.stream
	m.mu.Unlock()

	for _, valueSubscription := range subscriptions {
		valueSubscription.Cancel()
	}
	if stream == nil {
		return nil
	}
	err := stream.Close()
	<-m.streamDone
	return err
}
