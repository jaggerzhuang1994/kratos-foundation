package context

import "context"

// Hook context 钩子接口，用于向 context 注入自定义数据
type Hook interface {
	// WithContext 注册一个 context 修改函数
	WithContext(func(ctx context.Context) context.Context)
}

// hookInternal 内部接口，仅在 context 包内使用，用于读取已注册的 context 修改函数
type hookInternal interface {
	getWithContext() []func(context.Context) context.Context
}

type hook struct {
	withContext []func(context.Context) context.Context
}

func NewHook() Hook {
	return &hook{}
}

func (h *hook) WithContext(withContext func(context.Context) context.Context) {
	h.withContext = append(h.withContext, withContext)
}

func (h *hook) getWithContext() []func(context.Context) context.Context {
	return h.withContext
}
