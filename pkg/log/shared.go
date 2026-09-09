package log

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

var errSharedStateReleased = errors.New("shared state is released")

// configState 保存一份不可变的 Config 版本。
type configState struct {
	version uint64

	output      *outputLogger
	level       kratoslog.Level
	filterEmpty bool
	filterKeys  []string
	callerDepth int
	timeFormat  string
	msgKey      string

	release func()
}

// customState 保存一份不可变的进程级自定义配置快照。
type customState struct {
	version uint64

	level       *kratoslog.Level
	filterEmpty *bool
	filterKeys  []string
	kv          []any
	callerDepth int
	timeFormat  string
	msgKey      string
}

// SharedState 保存根 Logger 与派生 Logger 共享的版本化状态。
type SharedState struct {
	config atomic.Pointer[configState]
	custom atomic.Pointer[customState]
}

var _ UpdateLogger = (*SharedState)(nil)

// NewSharedState 创建首个 Config 版本并持有输出资源，不修改全局 Logger。
func NewSharedState(config Config) (*SharedState, func(), error) {
	state, err := newConfigState(config)
	if err != nil {
		return nil, nil, err
	}

	shared := &SharedState{}
	shared.config.Store(state)
	shared.custom.Store(&customState{})

	cleanup := func() {
		old := shared.config.Swap(nil)
		if old != nil {
			old.release()
		}
	}

	return shared, cleanup, nil
}

// Update 完整构建并原子发布下一个 Config 版本，成功后释放旧输出。
func (s *SharedState) Update(config Config) error {
	for {
		old := s.config.Load()
		if old == nil {
			return errSharedStateReleased
		}

		next, err := newConfigState(config)
		if err != nil {
			return err
		}
		next.version = old.version + 1

		if s.config.CompareAndSwap(old, next) {
			old.release()
			return nil
		}
		next.release()
	}
}

func newConfigState(config Config) (*configState, error) {
	// 复制所有 slice，避免运行期配置引用调用方内存。
	config.FilterKeys = append([]string(nil), config.FilterKeys...)
	config.Std.FilterKeys = append([]string(nil), config.Std.FilterKeys...)
	config.File.FilterKeys = append([]string(nil), config.File.FilterKeys...)

	if err := validateConfig(config); err != nil {
		return nil, err
	}

	output, release, err := newOutputLogger(config)
	if err != nil {
		return nil, err
	}

	return &configState{
		output:      output,
		level:       config.Level,
		filterEmpty: config.FilterEmpty,
		filterKeys:  append([]string(nil), config.FilterKeys...),
		callerDepth: defaultCallerDepth,
		timeFormat:  config.TimeFormat,
		release:     release,
		msgKey:      defaultMsgKey,
	}, nil
}

// WithLevel 设置进程级最低日志级别。
func (s *SharedState) WithLevel(level kratoslog.Level) error {
	return s.updateCustom(func(state *customState) error {
		if level < kratoslog.LevelDebug || level > kratoslog.LevelFatal {
			return fmt.Errorf("log level is invalid")
		}
		state.level = &level
		return nil
	})
}

// WithFilterEmpty 设置进程级空值过滤策略。
func (s *SharedState) WithFilterEmpty(filterEmpty bool) error {
	return s.updateCustom(func(state *customState) error {
		state.filterEmpty = &filterEmpty
		return nil
	})
}

// WithFilterKeys 增加进程级敏感字段过滤规则。
func (s *SharedState) WithFilterKeys(keys ...string) error {
	values := append([]string(nil), keys...)
	return s.updateCustom(func(state *customState) error {
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
func (s *SharedState) WithKV(keyvals ...any) error {
	values := append([]any(nil), keyvals...)
	return s.updateCustom(func(state *customState) error {
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

// WithCallerDepth 设置进程级 caller depth。
func (s *SharedState) WithCallerDepth(depth int) error {
	return s.updateCustom(func(state *customState) error {
		if depth <= 0 {
			return fmt.Errorf("log caller depth must be positive")
		}
		state.callerDepth = depth
		return nil
	})
}

// WithTimeFormat 设置进程级时间戳格式。
func (s *SharedState) WithTimeFormat(timeFormat string) error {
	return s.updateCustom(func(state *customState) error {
		if strings.TrimSpace(timeFormat) == "" {
			return fmt.Errorf("log time format is empty")
		}
		state.timeFormat = timeFormat
		return nil
	})
}

// WithMsgKey 设置消息类 Helper 使用的字段名。
func (s *SharedState) WithMsgKey(msgKey string) error {
	return s.updateCustom(func(state *customState) error {
		if strings.TrimSpace(msgKey) == "" {
			return fmt.Errorf("log msgKey is empty")
		}
		state.msgKey = strings.TrimSpace(msgKey)
		return nil
	})
}

// updateCustom 仅供本包方法发布快照；回调只操作副本，不执行外部调用。
func (s *SharedState) updateCustom(update func(*customState) error) error {
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
			return err
		}
		if s.custom.CompareAndSwap(old, next) {
			return nil
		}
		// 从最新快照重试，保留其他并发方法调用成功发布的设置。
	}
}
