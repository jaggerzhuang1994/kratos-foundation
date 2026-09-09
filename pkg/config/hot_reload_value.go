package config

import (
	"sync/atomic"

	"github.com/go-kratos/kratos/v2/log"
)

type hotReloadValueWrapper[T any] struct {
	val     *T
	version uint64
}

type HotReloadValue[T any] struct {
	value atomic.Pointer[hotReloadValueWrapper[T]]
}

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

func (d *HotReloadValue[T]) GetCurrent() (*T, uint64) {
	snapshot := d.value.Load()
	return snapshot.val, snapshot.version
}
