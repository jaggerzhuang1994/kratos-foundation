package config

import (
	"sync/atomic"

	"github.com/go-kratos/kratos/v2/log"
)

type hotReloadValueWrapper[T any] struct {
	val     *T
	version uint64
}

// HotReloadValue 原子发布最近一次成功解码的配置；读取结果为共享只读快照。
// 后续订阅错误保留旧值，不自动重新订阅，也不暴露订阅终止状态。
type HotReloadValue[T any] struct {
	value atomic.Pointer[hotReloadValueWrapper[T]]
}

// NewHotReloadValue 加载并订阅 key，最多接收一个与 T 对应的默认值。
// 返回的 cleanup 取消订阅；已开始的回调仍可能结束，容器继续保留最近快照。
// 初次加载或订阅失败时返回错误，后续错误记录 WARN 并保留旧值。
func NewHotReloadValue[T any](config Manager, key string, optionalDefault ...*T) (*HotReloadValue[T], func(), error) {
	init := new(T)
	defaultValue := make([]any, 0, len(optionalDefault))
	for _, v := range optionalDefault {
		defaultValue = append(defaultValue, v)
	}

	if err := config.Load(key, init, defaultValue...); err != nil {
		return nil, nil, err
	}

	hotReloadValue := &HotReloadValue[T]{}
	hotReloadValue.value.Store(&hotReloadValueWrapper[T]{val: init})

	cancel, err := config.Subscribe(key, new(T), func(_ string, value any, err error) {
		if err != nil {
			log.Warnf("config subscribe '%s' error: %v", key, err)
			return
		}
		for {
			old := hotReloadValue.value.Load()
			next := &hotReloadValueWrapper[T]{val: value.(*T), version: old.version + 1}

			if hotReloadValue.value.CompareAndSwap(old, next) {
				return
			}
		}
	}, defaultValue...)
	if err != nil {
		return nil, nil, err
	}

	return hotReloadValue, cancel, nil
}

// GetCurrent 返回一致的配置与版本对。配置及嵌套 map/slice 均只读，修改前须独立复制。
// 版本是本实例成功通知的计数，不是配置中心 revision，也不保证初始值为零。
func (d *HotReloadValue[T]) GetCurrent() (*T, uint64) {
	snapshot := d.value.Load()
	return snapshot.val, snapshot.version
}
