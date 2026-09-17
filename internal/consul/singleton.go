package consul

import "sync"

// singleton 管理进程共享的 Consul 客户端初始化结果。
type singleton struct {
	// once 保证进程客户端仅初始化一次，并同步发布初始化结果。
	once sync.Once
	// client 保存共享客户端，调用方仅借用，不单独关闭。
	client Client
	// disabled 缓存首次读取的禁用状态，不支持运行时切换。
	disabled bool
	// err 缓存首次初始化错误，失败后不会重新初始化。
	err error
}

var process singleton

// Get 返回进程共享客户端。首次调用读取 env 并探测，失败和禁用结果同样缓存。
// 禁用时返回 nil、true、nil；实际初始化失败返回 nil、false、err。
// 调用方只能借用，不得修改 SDK 配置或关闭其 transport；没有按驱动释放的 cleanup。
func Get() (client Client, disabled bool, err error) { return process.get() }

func (s *singleton) get() (Client, bool, error) {
	s.once.Do(func() {
		s.client, s.disabled, s.err = newClient()
	})
	return s.client, s.disabled, s.err
}
