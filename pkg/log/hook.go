package log

// Hook Logger 钩子接口，用于向 logger 注入全局键值对字段
// 允许在应用初始化时添加全局的日志上下文信息
type Hook interface {
	// With 注册全局键值对，这些键值对将被添加到所有日志中
	With(kv ...any)
}

// hook Hook 接口的实现，持有全局键值对数据
type hook struct {
	kv []any // 全局键值对
}

// NewHook 创建一个新的 Logger 钩子实例
func NewHook() Hook {
	return &hook{}
}

// With 添加全局键值对到钩子中
// 这些键值对将作为预设字段出现在所有日志输出中
func (h *hook) With(kv ...any) {
	h.kv = append(h.kv, kv...)
}
