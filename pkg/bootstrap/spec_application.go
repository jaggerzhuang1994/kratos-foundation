package bootstrap

import (
	"net/url"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
)

// AddContext 追加应用启动上下文的装饰函数。
func (s *Spec) AddContext(decorate app.ContextDecorator) error {
	return s.application.AddContext(decorate)
}

// AddMetadata 追加应用元数据。
func (s *Spec) AddMetadata(metadata map[string]string) error {
	return s.application.AddMetadata(metadata)
}

// AddEndpoints 追加对外公布的端点地址。
func (s *Spec) AddEndpoints(endpoints ...*url.URL) error {
	return s.application.AddEndpoints(endpoints...)
}

// AddSignals 指定触发应用退出的系统信号。
func (s *Spec) AddSignals(signals ...os.Signal) error {
	return s.application.AddSignals(signals...)
}

// BeforeStart 登记运行时启动前执行的业务钩子。
func (s *Spec) BeforeStart(hooks ...app.HookFunc) error {
	return s.application.BeforeStart(hooks...)
}

// AfterStart 登记所有运行时启动后执行的业务钩子。
func (s *Spec) AfterStart(hooks ...app.HookFunc) error {
	return s.application.AfterStart(hooks...)
}

// BeforeStop 登记运行时停止前执行的业务钩子。
func (s *Spec) BeforeStop(hooks ...app.HookFunc) error {
	return s.application.BeforeStop(hooks...)
}

// AfterStop 登记所有运行时停止后执行的业务钩子。
func (s *Spec) AfterStop(hooks ...app.HookFunc) error {
	return s.application.AfterStop(hooks...)
}
