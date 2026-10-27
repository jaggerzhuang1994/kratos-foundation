package log

import (
	"strings"
	"sync/atomic"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

// eventCounter 由 bootstrap.NewMetricsBootstrap 设置，把需要告警的异常事件计入指标。
var eventCounter atomic.Pointer[func(event string)]

// SetEventCounter 设置进程级异常事件计数回调，返回恢复前值的函数。只统计 WARN 及以上、事件名以
// .rejected、.panicked 或 .panic.recovered 结尾的日志（配置/策略被拒绝、panic 被恢复），
// 计数发生在日志级别过滤之前，调高模块日志级别不会漏计。事件名是代码中的固定常量，回调须并发安全且不阻塞。
func SetEventCounter(counter func(event string)) (restore func()) {
	var next *func(string)
	if counter != nil {
		next = &counter
	}
	previous := eventCounter.Swap(next)
	return func() { eventCounter.Store(previous) }
}

// CountedEvent 报告事件名是否属于计数范围。
func CountedEvent(event string) bool {
	return strings.HasSuffix(event, ".rejected") || strings.HasSuffix(event, ".panicked") || strings.HasSuffix(event, ".panic.recovered")
}

// observeEvent 在 WARN 及以上级别查找本次调用或实例字段中的 event，命中计数范围时回调。
func (l *logger) observeEvent(level kratoslog.Level, keyvals []any) {
	if level < kratoslog.LevelWarn {
		return
	}
	counter := eventCounter.Load()
	if counter == nil {
		return
	}
	event, ok := findEvent(keyvals)
	if !ok {
		event, ok = findEvent(l.kv)
	}
	if ok && CountedEvent(event) {
		(*counter)(event)
	}
}

func findEvent(keyvals []any) (string, bool) {
	for i := len(keyvals) - 2; i >= 0; i -= 2 {
		if key, ok := keyvals[i].(string); ok && key == "event" {
			event, ok := keyvals[i+1].(string)
			return event, ok
		}
	}
	return "", false
}
