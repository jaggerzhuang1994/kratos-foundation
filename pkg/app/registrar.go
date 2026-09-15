package app

import (
	"context"
	"errors"
	"sync"
	"time"

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

type supervisedRegistrar struct {
	registry.Registrar
	app     *App
	timeout time.Duration

	mu              sync.Mutex
	registered      bool
	deregistering   bool
	deregistered    bool
	deregisterDone  chan struct{}
	deregisterError error
	shutdownError   error
	instance        *registry.ServiceInstance
}

// Register 执行服务注册；若注册期间已收到停止请求，则立即补偿注销并收敛应用。
func (r *supervisedRegistrar) Register(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	if r.app.isStopping() {
		return r.app.stopAfterFailure(
			ctx,
			r.app.failureOr(errAppStopping),
		)
	}
	if err := r.Registrar.Register(ctx, instance); err != nil {
		return r.app.stopAfterFailure(
			ctx,
			r.app.startupCallbackError(ctx, err),
		)
	}

	r.mu.Lock()
	r.registered = true
	r.instance = instance
	stopping := r.app.isStopping()
	r.mu.Unlock()
	if !stopping {
		return nil
	}

	cleanupCtx := context.WithoutCancel(ctx)
	cancel := func() {}
	if r.timeout > 0 {
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, r.timeout)
	}
	defer cancel()
	_ = r.Deregister(cleanupCtx, instance)
	return r.app.stopAfterFailure(
		ctx,
		errAppStopping,
	)
}

// Deregister 幂等注销服务，并让并发调用共享第一次注销的稳定结果。
func (r *supervisedRegistrar) Deregister(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	r.mu.Lock()
	if !r.registered {
		r.mu.Unlock()
		return nil
	}
	if r.deregistered {
		err := r.deregisterError
		r.mu.Unlock()
		return err
	}
	if r.deregistering {
		// Kratos Stop 与注册完成后的补偿注销可能并发到达；等待同一次操作可避免
		// 对注册中心重复发送注销请求，同时仍尊重各调用方自己的 Context。
		done := r.deregisterDone
		r.mu.Unlock()
		select {
		case <-done:
			r.mu.Lock()
			err := r.deregisterError
			r.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.deregistering = true
	r.deregisterDone = make(chan struct{})
	done := r.deregisterDone
	if r.instance != nil {
		instance = r.instance
	}
	r.mu.Unlock()

	err := r.Registrar.Deregister(ctx, instance)

	r.mu.Lock()
	r.deregistering = false
	r.deregistered = true
	r.deregisterError = err
	close(done)
	r.mu.Unlock()
	return err
}

// finalError 汇总真正的注销错误和继续清理阶段记录的停机错误。
func (r *supervisedRegistrar) finalError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.deregisterError, r.shutdownError)
}

// recordShutdownError 在注销错误尚未成为主错误时保留额外停机错误。
func (r *supervisedRegistrar) recordShutdownError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	if r.deregisterError == nil {
		r.shutdownError = errors.Join(r.shutdownError, err)
	}
	r.mu.Unlock()
}

// newSupervisedRegistrar 为已启用的注册器增加停止协调和错误保留能力。
func newSupervisedRegistrar(
	registrar registry.Registrar,
	application *App,
	registrarTimeout time.Duration,
) *supervisedRegistrar {
	return &supervisedRegistrar{
		Registrar: registrar,
		app:       application,
		timeout:   registrarTimeout,
	}
}

var _ registry.Registrar = (*supervisedRegistrar)(nil)

// continuingRegistrar 适配 Kratos 的停机顺序。Kratos 遇到注销错误会在取消运行时前
// 提前返回，因此这里先保留真实错误，再向 Kratos 返回 nil 让其继续释放运行时。
type continuingRegistrar struct {
	registrar *supervisedRegistrar
}

// Register 将注册调用交给受监督注册器。
func (r *continuingRegistrar) Register(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	return r.registrar.Register(ctx, instance)
}

// Deregister 保留真实注销错误但向 Kratos 返回 nil，确保后续运行时仍会停止。
func (r *continuingRegistrar) Deregister(
	ctx context.Context,
	instance *registry.ServiceInstance,
) error {
	err := r.registrar.Deregister(ctx, instance)
	r.registrar.recordShutdownError(err)
	return nil
}

var _ registry.Registrar = (*continuingRegistrar)(nil)
