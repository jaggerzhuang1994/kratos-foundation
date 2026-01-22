package context

import "context"

// Hook Context 钩子接口，用于向 context 注入自定义数据
// 允许在应用初始化时扩展 context，添加额外的上下文信息
type Hook interface {
	// WithContext 注册一个 context 修改函数
	// 该函数接收当前 context 并返回修改后的 context
	WithContext(func(ctx context.Context) context.Context)
}

// hook Hook 接口的实现，持有所有 context 修改函数
type hook struct {
	withContext []func(context.Context) context.Context // context 修改函数列表
}

// NewHook 创建一个新的 Context 钩子实例
func NewHook() Hook {
	return &hook{}
}

// WithContext 添加一个 context 修改函数到钩子中
// 这些函数将在 NewContext 中按注册顺序依次执行
func (h *hook) WithContext(withContext func(context.Context) context.Context) {
	h.withContext = append(h.withContext, withContext)
}
