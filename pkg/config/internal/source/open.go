package source

import (
	"context"
	"errors"
	"fmt"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
)

// Open 先注册全部 watcher，再按 Sources 顺序加载完整初始状态。
func Open(sources []kratosconfig.Source) ([]*kratosconfig.KeyValue, *Stream, error) {
	sources = append([]kratosconfig.Source(nil), sources...)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &Stream{
		ctx:       ctx,
		cancel:    cancel,
		updates:   make(chan sourceUpdate),
		sources:   sources,
		watchers:  make([]kratosconfig.Watcher, 0, len(sources)),
		cached:    make([][]*kratosconfig.KeyValue, len(sources)),
		closeDone: make(chan struct{}),
	}

	for index, configSource := range sources {
		if configSource == nil {
			cancel()
			return nil, nil, errors.Join(
				fmt.Errorf("watch config source %d: source is nil", index),
				stopWatchers(stream.watchers),
			)
		}
		sourceWatcher, err := configSource.Watch()
		if err != nil {
			cancel()
			return nil, nil, errors.Join(
				fmt.Errorf("watch config source %d: %w", index, err),
				stopWatchers(stream.watchers),
			)
		}
		if sourceWatcher == nil {
			cancel()
			return nil, nil, errors.Join(
				fmt.Errorf("watch config source %d: watcher is nil", index),
				stopWatchers(stream.watchers),
			)
		}
		stream.watchers = append(stream.watchers, sourceWatcher)
	}

	for index, configSource := range sources {
		values, err := configSource.Load()
		if err != nil {
			cancel()
			return nil, nil, errors.Join(
				fmt.Errorf("load config source %d: %w", index, err),
				stopWatchers(stream.watchers),
			)
		}
		stream.cached[index] = cloneKeyValues(values)
	}
	initial := stream.snapshot()
	stream.workers.Add(len(stream.watchers))
	for index, sourceWatcher := range stream.watchers {
		go stream.watch(index, sourceWatcher)
	}
	return initial, stream, nil
}

func stopWatchers(watchers []kratosconfig.Watcher) error {
	stopErrors := make([]error, 0, len(watchers))
	for index := len(watchers) - 1; index >= 0; index-- {
		if err := watchers[index].Stop(); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop config watcher %d: %w", index, err))
		}
	}
	return errors.Join(stopErrors...)
}

func cloneKeyValues(values []*kratosconfig.KeyValue) []*kratosconfig.KeyValue {
	result := make([]*kratosconfig.KeyValue, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		next := *value
		next.Value = append([]byte(nil), value.Value...)
		result = append(result, &next)
	}
	return result
}
