package config

import (
	"sync/atomic"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

type hotReloadValueWrapper[T any] struct {
	// val 最近成功解码的配置，只读且包含嵌套字段。
	val *T
	// version 本容器成功通知计数，包含首次回放，不是配置中心版本。
	version uint64
}

// HotReloadValue 提供可热更新的配置读取，返回共享只读快照。
// 业务解码错误保留旧值；不报告来源健康状态。提供默认值时可从缺失 key 开始监听，后续新增配置会更新容器。
type HotReloadValue[T any] struct {
	// value 原子发布最近一次成功解码的配置与版本对，须经构造函数初始化。
	value atomic.Pointer[hotReloadValueWrapper[T]]
}

// NewHotReloadValue 先订阅再加载 key，最多接收一个与 T 对应的默认值。
// 返回的 cleanup 取消订阅；已开始的回调仍可能结束，容器继续保留最近快照。
// 初次加载或订阅失败时返回错误，后续错误记录 WARN 并保留旧值。
func NewHotReloadValue[T any](config Manager, key string, optionalDefault ...*T) (*HotReloadValue[T], func(), error) {
	init := new(T)
	defaultValue := make([]any, 0, len(optionalDefault))
	for _, v := range optionalDefault {
		defaultValue = append(defaultValue, v)
	}

	hotReloadValue := &HotReloadValue[T]{}
	// 先登记订阅关闭初始化窗口；占位快照仅在构造期间使用，不对调用方暴露。
	initial := &hotReloadValueWrapper[T]{val: new(T)}
	hotReloadValue.value.Store(initial)

	cancel, err := config.Subscribe(key, new(T), func(_ string, value any, err error) {
		if err != nil {
			log.WithModule("config").With(
				"key", key,
				"error", err,
			).Warn("Failed to update the subscribed configuration value; retaining the previous value")
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

	if err := config.Load(key, init, defaultValue...); err != nil {
		cancel()
		return nil, nil, err
	}
	// 已有通知发布新快照时保留它，避免较早读取的初值覆盖更新。
	hotReloadValue.value.CompareAndSwap(initial, &hotReloadValueWrapper[T]{val: init})

	return hotReloadValue, cancel, nil
}

// GetCurrent 返回一致的配置与版本对。配置及嵌套 map/slice 均只读，修改前须独立复制。
// 版本是本实例成功通知的计数，不是配置中心 revision；包含首次异步回放，构造完成时不保证为零。
func (d *HotReloadValue[T]) GetCurrent() (*T, uint64) {
	snapshot := d.value.Load()
	return snapshot.val, snapshot.version
}
