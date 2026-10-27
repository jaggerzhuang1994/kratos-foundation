package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-kratos/kratos/v2/registry"
	foundationregistry "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/registry"
)

// NewRegistrar 在组装阶段按 app.registry 解析注册能力；省略或空值使用 default 实例。
// App 继续接收 Registrar 接口，资源由 Registry Factory 的 cleanup 释放。
func NewRegistrar(settings Config, factory *foundationregistry.Factory) (registry.Registrar, error) {
	name := settings.GetRegistry()
	if name == "" {
		name = "default"
	}
	return factory.Registrar(name)
}

// register 在全部 Runtime 启动后按顺序注册全部实例。已请求停止时跳过；任一实例注册失败时注销已注册的实例，
// 避免注册中心残留部分名字。注册期间到达的停止请求可能已让 Stop 跳过后注册的实例，
// 因此每个实例注册成功后若发现正在停止，由这里补偿注销。
func (a *App) register(instances []*registry.ServiceInstance) error {
	if a.registrar == nil {
		return nil
	}
	if a.isStopping() {
		return errAppStopping
	}
	for _, instance := range instances {
		ctx, cancel := context.WithTimeout(a.runCtx, a.registrarTimeout)
		err := a.registrar.Register(ctx, instance)
		if err != nil {
			err = a.startupCallbackError(ctx, err)
			cancel()
			a.deregister()
			return err
		}
		cancel()
		a.mu.Lock()
		a.registered = append(a.registered, instance)
		a.mu.Unlock()
		// stopping 在 Stop 注销之前发布；这里在记录实例之后读取，
		// 因此 Stop 与本函数至少有一方看到对方的状态，注销不会遗漏。
		if a.isStopping() {
			a.deregister()
			return errAppStopping
		}
	}
	return nil
}

// deregister 注销尚未注销的已注册实例，每个实例最多注销一次，结果参与最终错误汇总；未注册时不调用注册中心。
// 使用不受停止取消影响的 Context，每次注销受 registrar_timeout 约束。
func (a *App) deregister() {
	a.mu.Lock()
	pending := slices.Clone(a.registered[a.deregisteredCount:])
	a.deregisteredCount = len(a.registered)
	a.mu.Unlock()

	var errs []error
	for _, instance := range pending {
		ctx, cancel := context.WithTimeout(a.base, a.registrarTimeout)
		if err := a.registrar.Deregister(ctx, instance); err != nil {
			errs = append(errs, fmt.Errorf("deregister %s: %w", instance.Name, err))
		}
		cancel()
	}
	if len(errs) == 0 {
		return
	}
	a.mu.Lock()
	a.deregisterErr = errors.Join(append([]error{a.deregisterErr}, errs...)...)
	a.mu.Unlock()
}
