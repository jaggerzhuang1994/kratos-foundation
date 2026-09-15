package config

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/compose-spec/compose-go/v2/template"
)

// preprocessedSource 不改变来源格式与合并语义，只复制并展开业务源模板。
// watcher 在构造完成前记录，之后只读；停止由 Manager 的 closeOnce 串行驱动。
type preprocessedSource struct {
	source  Source
	expand  bool
	watcher Watcher
	once    sync.Once
	stopErr error
}

func (s *preprocessedSource) Load() ([]*KeyValue, error) {
	values, err := s.source.Load()
	if err != nil {
		return nil, err
	}
	return s.preprocess(values)
}

func (s *preprocessedSource) Watch() (Watcher, error) {
	watcher, err := s.source.Watch()
	if err != nil {
		return nil, err
	}
	if watcher == nil {
		return nil, fmt.Errorf("config source returned nil watcher")
	}
	s.watcher = watcher
	return &preprocessedWatcher{source: s}, nil
}

func (s *preprocessedSource) preprocess(values []*KeyValue) ([]*KeyValue, error) {
	result := make([]*KeyValue, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		next := *value
		next.Value = append([]byte(nil), value.Value...)
		if s.expand {
			key, err := expandEnvironment(next.Key)
			if err != nil {
				return nil, fmt.Errorf("expand config key: %w", err)
			}
			content, err := expandEnvironment(string(next.Value))
			if err != nil {
				return nil, fmt.Errorf("expand config value: %w", err)
			}
			next.Key, next.Value = key, []byte(content)
		}
		result = append(result, &next)
	}
	return result, nil
}

func (s *preprocessedSource) stop() error {
	s.once.Do(func() {
		if s.watcher != nil {
			s.stopErr = s.watcher.Stop()
		}
	})
	return s.stopErr
}

type preprocessedWatcher struct{ source *preprocessedSource }

func (w *preprocessedWatcher) Next() ([]*KeyValue, error) {
	values, err := w.source.watcher.Next()
	if err != nil {
		return nil, err
	}
	return w.source.preprocess(values)
}

func (w *preprocessedWatcher) Stop() error { return w.source.stop() }

func expandEnvironment(value string) (string, error) {
	expanded, err := template.SubstituteWithOptions(value, os.LookupEnv, template.WithoutLogging)
	if err != nil {
		var invalid *template.InvalidTemplateError
		if errors.As(err, &invalid) {
			return "", errors.New("invalid environment template")
		}
	}
	return expanded, err
}
