// Package testconfig 提供供组件测试复用的 config.Manager fixture。
package testconfig

import (
	"context"
	"strconv"
	"sync"
	"testing"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	configtext "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// New 构造只包含一个 protobuf 顶层配置的 Manager。
func New(t testing.TB, key string, value proto.Message) config.Manager {
	t.Helper()
	return NewMany(t, map[string]proto.Message{key: value})
}

// NewMany 构造包含多个 protobuf 顶层配置的 Manager。
func NewMany(t testing.TB, values map[string]proto.Message) config.Manager {
	t.Helper()
	sources := make(config.Sources, 0, len(values))
	for key, value := range values {
		source, err := configtext.NewSource(
			key,
			config.JSONFormat,
			string(marshalSection(t, key, value)),
		)
		if err != nil {
			t.Fatalf("create %s config source: %v", key, err)
		}
		sources = append(sources, source)
	}
	manager, cleanup, err := config.NewManager(sources)
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	t.Cleanup(cleanup)
	return manager
}

// Empty 构造只包含 Foundation 空 section 基线的 Manager。
func Empty(t testing.TB) config.Manager {
	t.Helper()
	manager, cleanup, err := config.NewManager(nil)
	if err != nil {
		t.Fatalf("create empty config manager: %v", err)
	}
	t.Cleanup(cleanup)
	return manager
}

// MutableSource 提供测试主动推送的配置源，用于验证组件热更新。
type MutableSource struct {
	// key 标识 fixture 的顶层配置字段。
	key string
	// updates 传递测试配置快照，缓冲容量为 1；多个观察器竞争消费，不广播。
	updates chan []*kratosconfig.KeyValue

	// mu 保护当前序列化快照的并发读写。
	mu sync.RWMutex
	// value 保存当前 JSON 配置正文，加载时复制后返回。
	value []byte
}

var _ kratosconfig.Source = (*MutableSource)(nil)

// NewMutableSource 用初始 protobuf 值构造可变配置源。
func NewMutableSource(t testing.TB, key string, value proto.Message) *MutableSource {
	t.Helper()
	return &MutableSource{
		key:     key,
		updates: make(chan []*kratosconfig.KeyValue, 1),
		value:   marshalSection(t, key, value),
	}
}

// Update 更新当前 protobuf 快照并投递通知；仅在通知缓冲已满时等待观察器消费。
func (s *MutableSource) Update(t testing.TB, value proto.Message) {
	t.Helper()
	next := marshalSection(t, s.key, value)
	s.mu.Lock()
	s.value = next
	s.mu.Unlock()
	s.updates <- s.keyValues(next)
}

// Load 返回当前快照。
func (s *MutableSource) Load() ([]*kratosconfig.KeyValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keyValues(s.value), nil
}

// Watch 创建受测试控制的更新观察器。
func (s *MutableSource) Watch() (kratosconfig.Watcher, error) {
	return &mutableWatcher{
		updates: s.updates,
		done:    make(chan struct{}),
	}, nil
}

// keyValues 把 JSON 文本包装为 Kratos 配置源的单文件快照。
func (s *MutableSource) keyValues(value []byte) []*kratosconfig.KeyValue {
	return []*kratosconfig.KeyValue{{
		Key:    s.key + ".json",
		Format: config.JSONFormat,
		Value:  append([]byte(nil), value...),
	}}
}

type mutableWatcher struct {
	// updates 传递测试配置快照，缓冲容量为 1；多个观察器竞争消费，不广播。
	updates <-chan []*kratosconfig.KeyValue
	// done 观察器停止时关闭，唤醒等待中的 Next。
	done chan struct{}
	// once 保证观察器停止信号只关闭一次。
	once sync.Once
}

// Next 等待下一次配置更新或观察器停止。
func (w *mutableWatcher) Next() ([]*kratosconfig.KeyValue, error) {
	select {
	case values := <-w.updates:
		return values, nil
	case <-w.done:
		return nil, context.Canceled
	}
}

// Stop 幂等关闭观察器并唤醒等待中的 Next。
func (w *mutableWatcher) Stop() error {
	w.once.Do(func() {
		close(w.done)
	})
	return nil
}

// marshalSection 把 protobuf 值包装成以 key 为顶层字段的 JSON 文本。
func marshalSection(t testing.TB, key string, value proto.Message) []byte {
	t.Helper()
	content, err := protojson.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s config: %v", key, err)
	}
	return []byte("{" + strconv.Quote(key) + ":" + string(content) + "}")
}
