package config

import (
	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config/internal/decoder"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/pkg/errors"
)

var (
	// ErrRemovedField 表示 Foundation 已删除的配置字段仍然存在。
	ErrRemovedField = decoder.ErrRemovedField
	// ErrNotFound 表示请求的配置 key 不存在。
	ErrNotFound = kratosconfig.ErrNotFound
	// ErrManagerClosed 表示配置 Manager 已进入清理阶段。
	ErrManagerClosed = errors.New("config manager is closed")
	// ErrWatcherStopped 表示底层配置监听已永久终止，Manager 不再提供健康快照。
	ErrWatcherStopped = errors.New("config watcher is stopped")
	// ErrObserverOverloaded 表示单个订阅消费过慢，其有界更新队列已经写满。
	ErrObserverOverloaded = errors.New("config observer update queue is full")
)

const (
	// JSONFormat 表示 JSON 配置内容。
	JSONFormat = "json"
	// YAMLFormat 表示 YAML 配置内容。
	YAMLFormat = "yaml"
)

// Observer 接收一次配置快照。value 始终与订阅时传入的 prototype（原型）类型一致，
// 并且每次回调都会分配独立对象，调用方可以安全持有。
//
// 当加载或合并失败时，err 非 nil；这类错误不会终止订阅。若 errors.Is(err,
// ErrObserverOverloaded) 或 errors.Is(err, ErrWatcherStopped) 成立，则订阅已经停止。
type Observer func(key string, value any, err error)

// Manager 是组件读取有效配置的唯一入口。
// key 使用点分路径，可以指向任意层级，例如 "server.middleware.deadline"。
type Manager interface {
	// Load 将 key 的当前有效值写入已分配的非 nil 指针 target。
	// defaultValue 最多一个，必须与 target 的具体指针类型一致；配置不存在且未提供
	// 默认值时返回 ErrNotFound。每次写入前都会清空 target，调用方可安全复用对象。
	Load(key string, target any, defaultValue ...any) error

	// Subscribe 在返回前同步回放当前有效值，之后按顺序投递更新。
	// prototype 只用于固定回调值类型，每次回调都会收到独立的新对象；defaultValue
	// 的约束与 Load 相同。返回的 cancel 幂等，且不会等待已开始的业务回调。
	// 可恢复的解析或解码错误通过 observer 报告；ErrObserverOverloaded 和
	// ErrWatcherStopped 表示该订阅已经终止。
	Subscribe(
		key string,
		prototype any,
		observer Observer,
		defaultValue ...any,
	) (func(), error)
}

// Source 保留 Kratos 的配置源契约，便于文件、Consul 等实现直接接入。
type Source = kratosconfig.Source

// Sources 是有序配置源链，越靠后的配置源优先级越高。
type Sources []Source

// KeyValue 是 Source 加载和更新时交换的键值单元，别名避免自定义源重复导入 Kratos config。
type KeyValue = kratosconfig.KeyValue

// Watcher 是 Source 的变更监听器契约，别名使自定义源只依赖本包公开面。
type Watcher = kratosconfig.Watcher

// NewManager 注册并加载应用提供的全部有序配置源。
//
// Manager 独占 Source 的 Load、Watch 和 Watcher.Stop 生命周期。sources 后面的源
// 优先级更高：map 递归合并，其他类型和显式 null 整体覆盖。构造失败时会回滚已经
// 创建的 watcher；构造成功后，调用方必须在应用退出时调用幂等的 cleanup。
func NewManager(sources Sources) (Manager, func(), error) {
	manager, err := newManager(sources)
	if err != nil {
		return nil, nil, err
	}
	return manager, func() {
		if closeErr := manager.close(); closeErr != nil {
			log.WithModule("config").With("function", "NewManager.cleanup", "error", closeErr).Error("Failed to close the configuration manager")
		}
	}, nil
}

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
