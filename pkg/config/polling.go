package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/decoder"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type subscription struct {
	// caller 记录直接调用 Subscribe 的源码位置。
	caller string
	// key 订阅路径；空串表示根配置。
	key string
	// decoder 按订阅目标类型及默认值构造的解码器。
	decoder *decoder.Decoder
	// observer 接收配置值或解码错误的回调；同 Manager 串行执行，慢回调会阻塞后续通知。
	observer Observer
	// previous 保存上次扫描的不可变快照，即使未通知也推进，仅由轮询任务更新。
	previous map[string]any
	// pendingInitial 标记首次通知待发送，仅由轮询任务消费。
	pendingInitial bool
	// canceled 标记订阅已取消，由 Manager.mu 保护。
	canceled bool
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
	// 在登记处捕获直接调用点，避免异步通知时只能看到轮询栈；仅保留末级目录和文件名。
	caller := "<unknown>"
	if _, file, line, ok := runtime.Caller(1); ok {
		caller = fmt.Sprintf("%s/%s:%d", filepath.Base(filepath.Dir(file)), filepath.Base(file), line)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	sub := &subscription{caller: caller, key: key, decoder: valueDecoder, observer: observer, previous: m.snapshot, pendingInitial: true}
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
			// 只记录通知元数据，避免配置值中的凭据泄漏；此时尚未执行业务回调。
			key := sub.key
			if key == "" {
				key = "<root>" // 整份配置订阅使用非空标识，避免被日志空值过滤器移除。
			}
			// 首次回放与真实变更分开命名；正常路径省略默认标志和实现类型。
			logger := log.WithModule("config").With("key", key, "subscription", sub.caller)
			if !exists {
				logger = logger.With("found", false)
			}
			if initial {
				logger.Info("config.watch")
			} else {
				paths, truncated := changedPaths(sub.key, previous, existed, value, exists)
				logger = logger.With("changed_paths", paths)
				if truncated {
					logger = logger.With("paths_truncated", true)
				}
				logger.Info("config.change")
			}
			sub.notify(value, exists)
		}
	}
}

func (s *subscription) notify(value any, found bool) {
	// 单个业务回调 panic 不应终止整个 Manager 的轮询，也不重试本次通知。
	defer func() {
		if recover() != nil {
			log.WithModule("config").With("key", s.key, "subscription", s.caller).
				Error("Configuration observer panicked; continuing polling")
		}
	}()
	target := s.decoder.NewTarget()
	s.observer(s.key, target, s.decoder.Apply(value, found, target))
}
