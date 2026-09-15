package config

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/decoder"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type subscription struct {
	key            string
	decoder        *decoder.Decoder
	observer       Observer
	previous       map[string]any // 仅由单个轮询任务更新，始终指向不可变快照。
	pendingInitial bool           // 仅由轮询任务消费，首次扫描即通知当前值。
	canceled       bool           // 由 Manager.mu 保护。
}

func pollInterval() (time.Duration, error) {
	value, set := os.LookupEnv("CONFIG_POLL_INTERVAL")
	if !set {
		return time.Second, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		return 0, fmt.Errorf("CONFIG_POLL_INTERVAL must be a positive duration")
	}
	return interval, nil
}

func (m *manager) Subscribe(key string, prototype any, observer Observer, defaults ...any) (func(), error) {
	if observer == nil {
		return nil, fmt.Errorf("config observer is nil")
	}
	valueDecoder, err := decoder.New(prototype, defaults)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	sub := &subscription{key: key, decoder: valueDecoder, observer: observer, previous: m.snapshot, pendingInitial: true}
	m.subs = append(m.subs, sub)
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		sub.canceled = true
		if index := slices.Index(m.subs, sub); index >= 0 {
			m.subs = slices.Delete(m.subs, index, index+1)
		}
	}, nil
}

// run 独占回调执行权；慢回调阻塞本 Manager 的后续通知，ticker 不积压扫描任务。
// cleanup 取消任务，不等待业务回调；在途回调返回后任务退出。
func (m *manager) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func (m *manager) poll() {
	var next map[string]any
	if err := m.backend.Scan(&next); err != nil {
		log.WithModule("config").With("error", err).
			Error("Failed to scan configuration; retaining the previous snapshot")
		return
	}
	// 只在锁内发布快照并复制订阅表；扫描、比较、解码及业务回调均不持锁。
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.snapshot = next
	subs := slices.Clone(m.subs)
	m.mu.Unlock()
	for _, sub := range subs {
		previous, existed := lookup(sub.previous, sub.key)
		value, exists := lookup(next, sub.key)
		sub.previous = next
		// 首次通知读取本轮快照，关闭业务初次 Load 与登记之间的更新窗口。
		initial := sub.pendingInitial
		sub.pendingInitial = false
		if !initial && existed == exists && reflect.DeepEqual(previous, value) {
			continue
		}
		m.mu.Lock()
		active := !m.closed && !sub.canceled
		m.mu.Unlock()
		if active {
			sub.notify(value, exists)
		}
	}
}

func (s *subscription) notify(value any, found bool) {
	// 单个业务回调 panic 不应终止整个 Manager 的轮询，也不重试本次通知。
	defer func() {
		if recover() != nil {
			log.WithModule("config").With("key", s.key).
				Error("Configuration observer panicked; continuing polling")
		}
	}()
	target := s.decoder.NewTarget()
	s.observer(s.key, target, s.decoder.Apply(value, found, target))
}
