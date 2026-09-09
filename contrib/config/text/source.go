// Package text 提供不可变的内存文本配置源。
package text

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

type source struct {
	key     string
	format  string
	content string
}

// Source 是供依赖注入区分内存文本配置源的独立类型。
type Source config.Source

// NewSource 返回指定键名和格式的不可变内存配置源。
func NewSource(key, format, content string) (Source, error) {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return nil, errors.New("text config source key is required")
	}
	if trimmedKey != key {
		return nil, errors.New("text config source key has surrounding whitespace")
	}
	trimmedFormat := strings.TrimSpace(format)
	if trimmedFormat == "" {
		return nil, errors.New("text config source format is required")
	}
	if trimmedFormat != format {
		return nil, errors.New("text config source format has surrounding whitespace")
	}
	return &source{key: key, format: format, content: content}, nil
}

// Load 每次返回新的键值对和字节切片，避免调用方修改后污染后续读取。
func (s *source) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{{
		Key:    s.key,
		Format: s.format,
		Value:  []byte(s.content),
	}}, nil
}

// Watch 返回可独立停止的静态监听器；文本源不会自行产生更新。
func (*source) Watch() (config.Watcher, error) {
	return newIdleWatcher(), nil
}

type idleWatcher struct {
	done chan struct{}
	once sync.Once
}

// newIdleWatcher 为每个订阅分配独立的停止信号，避免一个订阅影响其他订阅。
func newIdleWatcher() *idleWatcher {
	return &idleWatcher{done: make(chan struct{})}
}

// Next 在静态源上等待 Stop，然后用 context.Canceled 表达正常结束。
func (w *idleWatcher) Next() ([]*config.KeyValue, error) {
	<-w.done
	return nil, context.Canceled
}

// Stop 幂等地结束当前监听，并唤醒正在等待的 Next。
func (w *idleWatcher) Stop() error {
	w.once.Do(func() {
		close(w.done)
	})
	return nil
}
