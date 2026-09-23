package log

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

var processState = func() *sharedState {
	state := &sharedState{}
	state.custom.Store(&customState{})
	return state
}()

// RegisterFields 登记进程级组件元数据；业务实例字段使用 With。
// 仅供组装层登记 service、trace 等字段，不改变运行期日志策略。
func RegisterFields(values ...any) { processState.WithKV(values...) }

// customState 保存一份不可变的进程级自定义配置快照。
type customState struct {
	// version 共享策略版本，用于使派生 Logger 缓存失效。
	version uint64
	// policy 已复制的运行期策略。
	policy *RuntimeConfig

	// filterKeys 共享根过滤覆盖；nil 使用实例配置。
	filterKeys []string
	// kv 进程共享日志字段；仅复制切片，字段值引用的对象不深拷贝。
	kv []any
}

// sharedState 保存共享策略并借用活动输出，资源仍由每个实例的 cleanup 释放。
type sharedState struct {
	// custom 原子发布的共享策略与字段快照。
	custom atomic.Pointer[customState]
	// gate 保护输出登记与提交，并让一条日志使用同一代策略和输出。
	gate sync.RWMutex
	// owners 活动输出入口及各自启动环境，由 gate 保护。
	owners map[*outputLogger]envConfig
	// epoch 输出登记及策略提交序号，用于拒绝过期准备结果，由 gate 保护。
	epoch uint64
}

// WithKV 增加进程级字段；重复字符串 key 使用后声明的值，module 在输出组装时忽略。
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
			// 空切片代表清空 env 过滤，复制时必须保留其与 nil 的区别。
			next.filterKeys = slices.Clone(old.filterKeys)
		}
		if err := update(next); err != nil {
			WithModule("log").With("state", field, "error", err).Warn("rejected invalid logging settings; retaining the previous settings")
			return
		}
		if s.custom.CompareAndSwap(old, next) {
			return
		}
		// 从最新快照重试，保留其他并发方法调用成功发布的设置。
	}
}
