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
	// value 原子发布最近一次成功解码的配置与版本对，由构造函数初始化。
	value atomic.Pointer[hotReloadValueWrapper[T]]
}

// NewHotReloadValue 先加载 key 作为初值再订阅，最多接收一个与 T 对应的默认值。
// Manager 在登记后的下一轮扫描回放当前值，覆盖加载与登记之间可能发生的更新。
// 返回的 cleanup 取消订阅；已开始的回调仍可能结束，容器继续保留最近快照。
// 初次加载或订阅失败时返回错误，后续错误记录 WARN 并保留旧值。
func NewHotReloadValue[T any](config Manager, key string, optionalDefault ...*T) (*HotReloadValue[T], func(), error) {
	defaultValue := make([]any, 0, len(optionalDefault))
	for _, v := range optionalDefault {
		defaultValue = append(defaultValue, v)
	}
	initial := new(T)
	if err := config.Load(key, initial, defaultValue...); err != nil {
		return nil, nil, err
	}
	hotReloadValue := &HotReloadValue[T]{}
	hotReloadValue.value.Store(&hotReloadValueWrapper[T]{val: initial})

	cancel, err := config.Subscribe(key, new(T), func(_ string, value any, err error) {
		if err != nil {
			log.WithModule("config").With(
				"event", "config.value.rejected",
				"key", displayKey(key),
				"error", err,
			).Warn("config value rejected; keeping previous")
			return
		}
		// Manager 串行回调同一订阅，回调是构造完成后的唯一写入方，无需比较并交换。
		version := hotReloadValue.value.Load().version + 1
		hotReloadValue.value.Store(&hotReloadValueWrapper[T]{val: value.(*T), version: version})
	}, defaultValue...)
	if err != nil {
		return nil, nil, err
	}
	return hotReloadValue, cancel, nil
}

// GetCurrent 返回一致的配置与版本对。配置及嵌套 map/slice 均只读，修改前须独立复制。
// 版本是本实例成功通知的计数，不是配置中心 revision；包含首次异步回放，构造完成时不保证为零。
func (d *HotReloadValue[T]) GetCurrent() (*T, uint64) {
	snapshot := d.value.Load()
	return snapshot.val, snapshot.version
}
