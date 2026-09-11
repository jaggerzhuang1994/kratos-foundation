package log

import (
	"fmt"
	"strings"
	"sync/atomic"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

var processState = func() *sharedState {
	state := &sharedState{}
	state.custom.Store(&customState{})
	return state
}()

// WithLevel 修改进程共享最低日志级别。
func WithLevel(level kratoslog.Level) { processState.WithLevel(level) }

// WithFilterEmpty 修改进程共享空字段过滤策略。
func WithFilterEmpty(value bool) { processState.WithFilterEmpty(value) }

// WithFilterKeys 修改进程共享字段过滤列表。
func WithFilterKeys(keys ...string) { processState.WithFilterKeys(keys...) }

// WithKV 合并进程共享日志字段。
func WithKV(values ...any) { processState.WithKV(values...) }

// WithTimeFormat 修改进程共享时间格式。
func WithTimeFormat(format string) { processState.WithTimeFormat(format) }

// WithMsgKey 修改进程共享消息字段名。
func WithMsgKey(key string) { processState.WithMsgKey(key) }

// customState 保存一份不可变的进程级自定义配置快照。
type customState struct {
	version uint64

	level       *kratoslog.Level
	filterEmpty *bool
	filterKeys  []string
	kv          []any
	timeFormat  string
	msgKey      string
}

// sharedState 仅保存进程共享的非资源配置，不拥有输出、cleanup 或引用计数。
type sharedState struct {
	custom atomic.Pointer[customState]
}

// WithLevel 设置进程级最低日志级别。
func (s *sharedState) WithLevel(level kratoslog.Level) {
	s.updateCustom("level", func(state *customState) error {
		if level < kratoslog.LevelDebug || level > kratoslog.LevelFatal {
			return fmt.Errorf("log level is invalid")
		}
		state.level = &level
		return nil
	})
}

// WithFilterEmpty 设置进程级空值过滤策略。
func (s *sharedState) WithFilterEmpty(filterEmpty bool) {
	s.updateCustom("filter_empty", func(state *customState) error {
		state.filterEmpty = &filterEmpty
		return nil
	})
}

// WithFilterKeys 增加进程级敏感字段过滤规则。
func (s *sharedState) WithFilterKeys(keys ...string) {
	values := append([]string(nil), keys...)
	s.updateCustom("filter_keys", func(state *customState) error {
		seen := make(map[string]struct{}, len(state.filterKeys)+len(values))
		for _, key := range state.filterKeys {
			seen[key] = struct{}{}
		}
		for _, key := range values {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("log filter key is empty")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("log filter key %q is duplicated", key)
			}
			seen[key] = struct{}{}
			state.filterKeys = append(state.filterKeys, key)
		}
		return nil
	})
}

// WithKV 增加进程级字段；重复字符串 key 使用后声明的值。
func (s *sharedState) WithKV(keyvals ...any) {
	values := append([]any(nil), keyvals...)
	s.updateCustom("kv", func(state *customState) error {
		if len(values)%2 != 0 {
			return fmt.Errorf("log KV requires key/value pairs")
		}
		positions := make(map[string]int, len(state.kv)/2+len(values)/2)
		for index := 0; index < len(state.kv); index += 2 {
			positions[state.kv[index].(string)] = index
		}
		for index := 0; index < len(values); index += 2 {
			key, ok := values[index].(string)
			if !ok {
				return fmt.Errorf("log KV key must be a string")
			}
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("log KV key is empty")
			}
			if position, exists := positions[key]; exists {
				state.kv[position+1] = values[index+1]
				continue
			}
			positions[key] = len(state.kv)
			state.kv = append(state.kv, key, values[index+1])
		}
		return nil
	})
}

// WithTimeFormat 设置进程级时间戳格式。
func (s *sharedState) WithTimeFormat(timeFormat string) {
	s.updateCustom("time_format", func(state *customState) error {
		if strings.TrimSpace(timeFormat) == "" {
			return fmt.Errorf("log time format is empty")
		}
		state.timeFormat = timeFormat
		return nil
	})
}

// WithMsgKey 设置消息类 Helper 使用的字段名。
func (s *sharedState) WithMsgKey(msgKey string) {
	s.updateCustom("msg_key", func(state *customState) error {
		if strings.TrimSpace(msgKey) == "" {
			return fmt.Errorf("log msgKey is empty")
		}
		state.msgKey = strings.TrimSpace(msgKey)
		return nil
	})
}

// updateCustom 仅供本包方法发布快照；校验失败保留原状态，并记录失败项和原因。
// 回调只操作副本，不执行外部调用；warning 在回调失败后记录。
func (s *sharedState) updateCustom(field string, update func(*customState) error) {
	for {
		old := s.custom.Load()
		next := &customState{version: 1}
		if old != nil {
			*next = *old
			next.version++
			// 复制可变切片，校验失败或 CAS 冲突均不影响已发布快照。
			next.kv = append([]any(nil), old.kv...)
			next.filterKeys = append([]string(nil), old.filterKeys...)
		}
		if err := update(next); err != nil {
			kratoslog.Warnw("msg", "log custom state update failed", "state", field, "error", err)
			return
		}
		if s.custom.CompareAndSwap(old, next) {
			return
		}
		// 从最新快照重试，保留其他并发方法调用成功发布的设置。
	}
}
