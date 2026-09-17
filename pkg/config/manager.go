package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	kratosenv "github.com/go-kratos/kratos/v2/config/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/decoder"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

var (
	// ErrRemovedField 表示 Foundation 已删除的配置字段仍然存在。
	ErrRemovedField = decoder.ErrRemovedField
	// ErrNotFound 表示请求的配置 key 不存在。
	ErrNotFound = kratosconfig.ErrNotFound
	// ErrManagerClosed 表示配置 Manager 已进入清理阶段。
	ErrManagerClosed = errors.New("config manager is closed")
)

const (
	// JSONFormat 表示 JSON 配置内容。
	JSONFormat = "json"
	// YAMLFormat 表示 YAML 配置内容。
	YAMLFormat = "yaml"
)

// Observer 接收按 prototype 解码的值；err 表示解码失败或缺失值无默认值。
// 来源加载、模板和监听错误由官方 Config 记录日志，不经此接口转发。
type Observer func(key string, value any, err error)

// Reader 提供配置读取能力，适合仅在构造期读取配置的驱动。
type Reader interface {
	Load(key string, target any, defaultValue ...any) error
}

// Manager 是组件读取有效配置的唯一入口。
// key 使用点分路径，可以指向任意层级，例如 "server.middleware.deadline"。
type Manager interface {
	// Load 将最近扫描快照中的 key 写入已分配的非 nil 指针 target。
	// defaultValue 最多一个，必须与 target 的具体指针类型一致；配置不存在且未提供
	// 默认值时返回 ErrNotFound。每次写入前都会清空 target，调用方可安全复用对象。
	Load(key string, target any, defaultValue ...any) error

	// Subscribe 登记独立订阅；下一轮成功 Scan 后异步回放当前值，允许 key 尚不存在或为 null。
	// Manager 每轮 Scan 后按登记顺序串行通知变化，不保证捕获轮询间的中间状态。
	// 同 key 可有多个订阅；cancel 幂等移除订阅，不等待已开始的回调。
	Subscribe(
		key string,
		prototype any,
		observer Observer,
		defaultValue ...any,
	) (func(), error)
}

// Source 保留 Kratos 的配置源契约，便于文件、Consul 等实现直接接入。
type Source = kratosconfig.Source

// Sources 按顺序进行初始加载；热更新按官方默认 merge 处理，不保证固定来源优先级。
type Sources []Source

// KeyValue 是 Source 加载和更新时交换的键值单元，别名避免自定义源重复导入 Kratos config。
type KeyValue = kratosconfig.KeyValue

// Watcher 是 Source 的变更监听器契约，别名使自定义源只依赖本包公开面。
type Watcher = kratosconfig.Watcher

// SourceLoader 在 Configuration 阶段创建配置源，不启动 watcher。
// 来源的 Load、Watch 和 Stop 统一由 Manager 管理；失败时构造器自行回滚。
type SourceLoader func() (Sources, error)

// NewSources 过滤未启用的 nil 配置源，同时保留原始顺序，便于应用组装可选配置源
// 时显式表达覆盖优先级。
func NewSources(sources ...Source) Sources {
	result := make(Sources, 0, len(sources))
	for _, source := range sources {
		if source != nil {
			result = append(result, source)
		}
	}
	return result
}

// NewManager 以官方 env.NewSource() 为最低优先级，加载应用提供的有序配置源。
//
// 使用官方 decoder、merge 和 resolver；业务源先预处理环境模板。
// CONFIG_POLL_INTERVAL 指定正数 duration（默认 1s），构造时读取一次。
// 构造失败停止已创建 watcher；成功后由组装层调用幂等 cleanup。
func NewManager(sources Sources) (Manager, func(), error) {
	manager, err := newManager(sources)
	if err != nil {
		return nil, nil, err
	}
	return manager, func() {
		if closeErr := manager.close(); closeErr != nil {
			log.WithModule("config").With("error", closeErr).Error("Failed to close the configuration manager")
		}
	}, nil
}

// manager 管理配置扫描与订阅通知。
type manager struct {
	// backend 合并和加载配置的 Kratos 后端。
	backend kratosconfig.Config
	// sources 经过复制及模板预处理的来源，由 Manager 负责停止。
	sources []*preprocessedSource
	// mu 保护关闭状态、订阅集合及共享快照。
	mu sync.Mutex
	// closed 是否已关闭，阻止后续加载和订阅。
	closed bool
	// snapshot 最近一次轮询发布的不可变配置树。
	snapshot map[string]any
	// subs 已登记的配置订阅。
	subs []*subscription
	// cancel 请求轮询任务退出；关闭不等待已开始的业务回调。
	cancel context.CancelFunc
	// closeOnce 确保关闭流程只执行一次。
	closeOnce sync.Once
	// closeErr 关闭流程的结果，供重复关闭返回。
	closeErr error

	// nextSubscriptionID 为本 Manager 分配日志关联编号，由 mu 保护。
	nextSubscriptionID uint64
}

func newManager(sources Sources) (*manager, error) {
	interval, err := pollInterval()
	if err != nil {
		return nil, err
	}
	m := &manager{}
	all := append(Sources{kratosenv.NewSource()}, sources...)
	wrapped := make([]kratosconfig.Source, 0, len(all))
	for i, source := range all {
		if source == nil {
			return nil, fmt.Errorf("config source %d is nil", i)
		}
		next := &preprocessedSource{source: source, expand: i != 0}
		m.sources = append(m.sources, next)
		wrapped = append(wrapped, next)
	}
	// Source 先展开环境模板，合并后继续使用官方 resolver 解析配置引用。
	m.backend = kratosconfig.New(kratosconfig.WithSource(wrapped...))
	if err := m.backend.Load(); err != nil {
		return nil, errors.Join(err, m.close())
	}
	if err := m.backend.Scan(&m.snapshot); err != nil {
		return nil, errors.Join(err, m.close())
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx, interval)
	return m, nil
}

func (m *manager) Load(key string, target any, defaults ...any) error {
	m.mu.Lock()
	closed, snapshot := m.closed, m.snapshot
	m.mu.Unlock()
	if closed {
		return ErrManagerClosed
	}
	valueDecoder, err := decoder.New(target, defaults)
	if err != nil {
		return fmt.Errorf("load config %q: %w", key, err)
	}
	value, found := lookup(snapshot, key)
	return valueDecoder.Apply(value, found, target)
}

func (m *manager) close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.subs = nil
		m.mu.Unlock()
		if m.cancel != nil {
			m.cancel()
		}
		// 官方 Close 遇首个 Stop 错误即返回；逐个关闭包装源保证后续来源也被释放。
		var errs []error
		if err := m.backend.Close(); err != nil {
			errs = append(errs, err)
		}
		for _, source := range m.sources {
			if err := source.stop(); err != nil && !errors.Is(errors.Join(errs...), err) {
				errs = append(errs, err)
			}
		}
		m.closeErr = errors.Join(errs...)
	})
	return m.closeErr
}

// lookup 只读访问不可变快照，存在性与 null 分开比较。
func lookup(root map[string]any, key string) (any, bool) {
	if key == "" {
		return root, true
	}
	var value any = root
	for part := range strings.SplitSeq(key, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}
