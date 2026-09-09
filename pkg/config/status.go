package config

import (
	"sort"
	"time"
)

// StatusReader 提供不依赖业务回调的运行状态；NewManager 返回的实例实现此接口。
// 独立于 Manager，已有自定义 Manager 不必实现观测能力。
type StatusReader interface{ Status() Status }

// Status 是无配置值、无原始错误文本的运行状态副本。
// Revision 是本进程接受快照的序号，不是配置中心版本；初始加载计为一次接受。
type Status struct {
	WatcherRunning   bool
	Closed           bool
	Revision         uint64
	AcceptedUpdates  uint64
	RejectedUpdates  uint64
	LastSuccess      time.Time
	LastErrorCode    string
	Overloads        uint64
	Callbacks        uint64
	CallbackDuration time.Duration
	Subscriptions    []SubscriptionStatus
}

// SubscriptionStatus 是仍注册的订阅状态；终止回调交付完成或 cancel 后移除。
// Overloads 是 Manager 生命周期累计值，因此移除订阅不会抹掉过载记录。
type SubscriptionStatus struct {
	Key          string
	Accepting    bool
	Pending      int
	Running      bool
	RunningSince time.Time
}

// Status 在现有锁下复制状态，在释放 Manager 锁后逐个读取订阅，避免嵌套持锁。
// 各订阅是独立采样，不承诺所有字段来自同一个全局原子时刻。
func (m *manager) Status() Status {
	m.mu.Lock()
	result := m.status
	result.Closed = m.closed
	result.WatcherRunning = !m.closed && m.watchErr == nil
	deliveries := make([]delivery, 0)
	for key, watch := range m.watches {
		for _, sub := range watch.subscriptions {
			deliveries = append(deliveries, delivery{subscription: sub})
			result.Subscriptions = append(result.Subscriptions, SubscriptionStatus{Key: key})
		}
	}
	m.mu.Unlock()
	for i, item := range deliveries {
		status := item.subscription.Status()
		result.Subscriptions[i].Accepting = status.Accepting
		result.Subscriptions[i].Pending = status.Pending
		result.Subscriptions[i].Running = status.Running
		result.Subscriptions[i].RunningSince = status.RunningSince
	}
	sort.SliceStable(result.Subscriptions, func(i, j int) bool { return result.Subscriptions[i].Key < result.Subscriptions[j].Key })
	return result
}

func (m *manager) recordCallback(start time.Time) {
	elapsed := time.Since(start)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Callbacks++
	m.status.CallbackDuration += elapsed
}

func (m *manager) recordRejected(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.RejectedUpdates++
	m.status.LastErrorCode = code
}
