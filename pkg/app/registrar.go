package app

import (
	"context"

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

// register 在全部 Runtime 启动后注册服务。已请求停止时跳过；注册期间到达的停止请求
// 可能已让 Stop 跳过注销，因此注册成功后若发现正在停止，由这里补偿注销一次。
func (a *App) register(instance *registry.ServiceInstance) error {
	if a.registrar == nil {
		return nil
	}
	if a.isStopping() {
		return errAppStopping
	}
	ctx, cancel := context.WithTimeout(a.runCtx, a.registrarTimeout)
	defer cancel()
	if err := a.registrar.Register(ctx, instance); err != nil {
		return a.startupCallbackError(ctx, err)
	}
	a.mu.Lock()
	a.registered = true
	a.mu.Unlock()
	// stopping 在 Stop 读取 registered 之前发布；这里在写入 registered 之后读取，
	// 因此 Stop 与本函数至少有一方看到对方的状态，注销不会遗漏。
	if a.isStopping() {
		a.deregister()
		return errAppStopping
	}
	return nil
}

// deregister 最多注销一次已注册的服务，结果参与最终错误汇总；未注册时不调用注册中心。
// 使用不受停止取消影响的 Context，并受 registrar_timeout 约束。
func (a *App) deregister() {
	a.mu.Lock()
	if !a.registered || a.deregistered {
		a.mu.Unlock()
		return
	}
	a.deregistered = true
	instance := a.instance
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(a.base, a.registrarTimeout)
	defer cancel()
	err := a.registrar.Deregister(ctx, instance)
	a.mu.Lock()
	a.deregisterErr = err
	a.mu.Unlock()
}
