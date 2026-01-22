// Package log 提供日志管理功能，包括 Logger Hook 机制。
//
// Log Hook 允许在应用初始化时向 logger 注入全局键值对字段，
// 这些字段会自动出现在所有日志输出中，无需手动添加。
//
// 使用示例：
//
//	logHook.With("service", "user-service")
//	logHook.With("version", "v1.0.0")
//	logHook.With("environment", "production")
//
//	// 之后所有日志都会自动包含这些字段
//	log.Info("处理请求")
//	// 输出: {"service":"user-service","version":"v1.0.0","environment":"production","msg":"处理请求"}
//
// 这种机制特别适合注入固定的上下文信息，如服务名、版本、环境等。
package log

import (
	"time"
)

// Hook Logger 钩子接口，用于向 logger 注入全局键值对字段
//
// Log Hook 提供了一种机制，允许在应用初始化阶段注册全局的键值对，
// 这些键值对会自动添加到所有日志输出中，作为结构化日志的一部分。
//
// 典型使用场景：
//   - 注入服务名称、版本号等固定信息
//   - 注入环境标识（dev/staging/production）
//   - 注入租户 ID 或应用标识
//   - 注入主机名、IP 地址等部署信息
//   - 注入集群、区域等部署拓扑信息
//
// 数据格式：
//
//	键值对应该以交替的 key, value 形式提供，例如：
//	With("service", "user-service", "version", "1.0.0")
//
// 注意：
//   - 全局字段会添加到每条日志中，避免添加过多字段
//   - 值的类型应该是可序列化的（字符串、数字、布尔等）
//   - 避免存储敏感信息（密码、密钥等）
//   - 全局字段在每次日志输出时都会被序列化，影响性能
type Hook interface {
	// With 注册全局键值对，这些键值对将被添加到所有日志中
	//
	// 参数：
	//   kv: 键值对，以交替的 key, value 形式提供
	//
	// 示例：
	//   // 单个键值对
	//   hook.With("service", "user-service")
	//
	//   // 多个键值对
	//   hook.With("service", "user-service", "version", "v1.0.0", "env", "production")
	//
	//   // 分多次调用
	//   hook.With("service", "user-service")
	//   hook.With("version", "v1.0.0")
	//   hook.With("env", "production")
	//
	// 注意：
	//   - 键应该是字符串类型
	//   - 值应该是可序列化的类型
	//   - 每次调用都会追加新的键值对，不会覆盖之前的
	//   - 相同的键会被添加多次，注意避免重复
	With(kv ...any)

	// GetKv 获取所有注册的全局键值对
	//
	// 返回的键值对以交替的 key, value 形式存储。
	// 此方法主要用于内部日志系统获取全局字段。
	GetKv() []any

	// GetLastUpdatedAt 获取最后一次更新的时间戳
	//
	// 返回的时间戳表示最后一次调用 With 方法的时间。
	// 可用于判断全局字段是否发生了变化。
	GetLastUpdatedAt() time.Time
}

// hook Hook 接口的实现，持有全局键值对数据
//
// 键值对按照添加顺序存储，可以动态追加新的字段。
// 时间戳记录最后一次更新的时间，用于追踪字段变化。
type hook struct {
	// 全局键值对，以交替的 key, value 形式存储
	// 例如: ["service", "user-service", "version", "1.0.0"]
	kv []any

	// 最后一次更新的时间戳
	// 每次调用 With 方法时都会更新
	timestamp time.Time
}

// NewHook 创建一个新的 Logger 钩子实例
//
// 返回的 Hook 实例初始为空，可以通过 With 方法添加键值对。
func NewHook() Hook {
	return &hook{
		timestamp: time.Now(),
	}
}

// With 添加全局键值对到钩子中
//
// 这些键值对将作为预设字段出现在所有日志输出中。
//
// 执行流程：
//  1. 将新的键值对追加到现有列表
//  2. 更新时间戳为当前时间
//  3. 后续的日志输出会自动包含这些字段
//
// 示例：
//
//	hook := NewHook()
//	hook.With("service", "user-service")
//	hook.With("version", "v1.0.0")
//	hook.With("env", "production")
//
//	// 在日志中使用
//	log.Info("启动应用") // 输出: {"service":"user-service","version":"v1.0.0","env":"production","msg":"启动应用"}
//
// 注意：
//   - 每次调用都会追加，不会替换现有的键值对
//   - 如果需要更新某个键的值，建议重建 Hook 实例
//   - 时间戳会在每次调用时更新
func (h *hook) With(kv ...any) {
	h.kv = append(h.kv, kv...)
	h.timestamp = time.Now()
}

// GetKv 获取所有注册的全局键值对
//
// 返回的切片以交替的 key, value 形式存储。
// 例如：["service", "user-service", "version", "1.0.0"]
//
// 此方法主要用于日志系统内部，将全局字段合并到日志输出中。
func (h *hook) GetKv() []any {
	return h.kv
}

// GetLastUpdatedAt 获取最后一次更新的时间戳
//
// 返回的时间戳表示最后一次调用 With 方法的时间。
// 可用于判断全局字段是否发生了变化，或者在日志中记录字段更新时间。
func (h *hook) GetLastUpdatedAt() time.Time {
	return h.timestamp
}
