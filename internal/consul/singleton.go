package consul

import "sync"

// singleton 固定首次初始化结果；Once 同时保证其他调用看到完整结果。
type singleton struct {
	once     sync.Once
	client   Client
	disabled bool
	err      error
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
