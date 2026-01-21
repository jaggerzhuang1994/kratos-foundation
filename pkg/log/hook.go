package log

// Hook logger 钩子接口，用于向 logger 注入全局 kv 字段
type Hook interface {
	// With 注册全局 kv 字段
	With(kv ...any)
}

// hookInternal 内部接口，仅在 log 包内使用，用于读取已注册的 kv 字段
type hookInternal interface {
	customKv() []any
}

type hook struct {
	kv []any
}

func NewHook() Hook {
	return &hook{}
}

func (h *hook) With(kv ...any) {
	h.kv = append(h.kv, kv...)
}

func (h *hook) customKv() []any {
	return h.kv
}
