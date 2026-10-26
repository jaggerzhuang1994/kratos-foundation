package client

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	stdgrpc "google.golang.org/grpc"
)

type retiredClient struct {
	// name 退役客户端的连接名称。
	name string
	// revision 退役配置版本号。
	revision uint64
	// protocol 退役客户端使用的协议。
	protocol config_pb.Protocol
	// reason 触发退役的原因。
	reason retireReason
	// result 已从共享状态分离的资源，在锁外释放。
	result clientResult
}

// AcquireClient 返回当前配置版本的客户端和调用级幂等 release；HTTP 服务发现不在此等待首个节点。
// 已构建的当前版本经只读索引无锁授予租约；首次构建、等待构建与关闭判断走 mu 保护的慢路径。
func (f *factory) AcquireClient(ctx context.Context, name string) (
	httpClient *kratoshttp.Client,
	grpcClient *stdgrpc.ClientConn,
	release func(),
	err error,
) {
	if name == "" {
		return nil, nil, nil, ErrInvalidClientName
	}
	if index := f.published.Load(); index != nil {
		// 索引可能略旧：版本若已退役则 tryAcquire 撤回并回到慢路径，不会租用已关闭的客户端。
		if version := (*index)[name]; version != nil {
			shard, ok, idle := version.tryAcquire()
			if ok {
				return f.lease(name, version, shard)
			}
			if idle {
				f.closeVersion(name, version)
			}
		}
	}

	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return nil, nil, nil, ErrFactoryClosed
		}

		slot := f.slots[name]
		if slot == nil {
			slot = &clientSlot{
				name: name,
				current: &clientVersion{
					revision: 1,
					spec:     newClientSpec(name, f.config.GetClients()[name], f.config, f.observabilityDefaults),
				},
			}
			f.slots[name] = slot
		}
		version := slot.current
		// 当前版本只在关闭或配置更新时退役，二者都在 mu 内替换 current，因此这里只需判断是否已构建。
		if version.client.present() {
			// 持有 mu 时当前版本不会退役，tryAcquire 必然成功。
			shard, _, _ := version.tryAcquire()
			f.mu.Unlock()
			return f.lease(name, version, shard)
		}

		call := slot.build
		if call == nil {
			// 成功发布后，构建 context 必须随客户端存活；cleanup 会单独取消未完成构建。
			buildCtx, cancel := context.WithCancel(context.Background())
			call = &buildCall{
				version: version,
				done:    make(chan struct{}),
				cancel:  cancel,
			}
			slot.build = call
			f.activities++
			f.mu.Unlock()
			go f.runBuild(slot, call, buildCtx)
		} else {
			f.mu.Unlock()
		}

		select {
		case <-call.done:
			f.mu.Lock()
			closed := f.closed
			current := f.slots[name].current
			stale := current != call.version
			buildErr := call.err
			f.mu.Unlock()

			switch {
			case closed:
				return nil, nil, nil, ErrFactoryClosed
			case stale:
				continue
			case buildErr != nil:
				return nil, nil, nil, buildErr
			default:
				continue
			}
		case <-ctx.Done():
			return nil, nil, nil, fmt.Errorf("acquire client %q: %w", name, ctx.Err())
		}
	}
}

// clientLease 是一次成功 Acquire 的租约；release 幂等，只归还一次。
type clientLease struct {
	factory  *factory
	name     string
	version  *clientVersion
	shard    int
	released atomic.Bool
}

// lease 在调用方已持有租约计数后返回客户端；租约存续期间版本不会被关闭，读取 client 无需加锁。
func (f *factory) lease(name string, version *clientVersion, shard int) (*kratoshttp.Client, *stdgrpc.ClientConn, func(), error) {
	lease := &clientLease{factory: f, name: name, version: version, shard: shard}
	return version.client.httpClient, version.client.grpcClient, lease.release, nil
}

// release 归还租约；归还后版本已退役且无剩余租约时关闭它，多个归还方同时满足条件也只关闭一次。
func (l *clientLease) release() {
	if !l.released.CompareAndSwap(false, true) {
		return
	}
	if l.version.releaseShard(l.shard) {
		l.factory.closeVersion(l.name, l.version)
	}
}

// publishLocked 必须持有 mu 调用；按 slots 重建已构建且未退役的当前版本索引并原子发布。
func (f *factory) publishLocked() {
	if f.closed {
		f.published.Store(nil)
		return
	}
	index := make(map[string]*clientVersion, len(f.slots))
	for name, slot := range f.slots {
		if slot.current.client.present() && !slot.current.retired.Load() {
			index[name] = slot.current
		}
	}
	f.published.Store(&index)
}

// retireLocked 必须持有 mu 调用；退役版本并返回需要立即关闭的客户端，仍有租约时登记到 draining。
func (f *factory) retireLocked(name string, version *clientVersion, reason retireReason) (retiredClient, bool) {
	idle := version.retire(reason)
	if !version.client.present() {
		return retiredClient{}, false
	}
	if !idle {
		f.draining[version] = name
		return retiredClient{}, false
	}
	return detachRetired(name, version), true
}

// closeVersion 关闭最后一个租约归还的已退役版本；强制关闭后晚到的归还不会重复关闭。
func (f *factory) closeVersion(name string, version *clientVersion) {
	f.mu.Lock()
	if !version.client.present() {
		f.mu.Unlock()
		return
	}
	retired := detachRetired(name, version)
	delete(f.draining, version)
	f.signalDrainedLocked()
	f.mu.Unlock()
	f.closeAndLog(retired)
}

func (f *factory) runBuild(slot *clientSlot, call *buildCall, ctx context.Context) {
	defer f.endActivity()

	version := call.version
	buildCancel := call.cancel
	result, buildErr := f.builder.build(ctx, version.spec)
	if buildErr == nil {
		buildErr = result.validate()
	}
	if buildErr != nil {
		buildCancel()
		f.mu.Lock()
		call.cancel = nil
		f.mu.Unlock()
		if closeErr := result.close(); closeErr != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("close rejected client result: %w", closeErr))
		}
		result = clientResult{}
	} else {
		result = result.withCancel(buildCancel)
	}

	var rejected *retiredClient
	published := false
	f.mu.Lock()
	if buildErr == nil {
		call.cancel = nil
	}
	if !f.closed && slot.current == version && slot.build == call {
		if buildErr == nil {
			version.client = result
			published = true
			f.publishLocked()
		} else {
			call.err = fmt.Errorf(
				"build client %q revision %d protocol %s target %q: %w",
				version.spec.name,
				version.revision,
				version.spec.protocol,
				version.spec.target,
				buildErr,
			)
		}
	} else if result.present() {
		reason := retireStaleBuild
		if f.closed && version.retired.Load() && version.retireReason == retireFactoryClosed {
			reason = retireFactoryClosed
		}
		rejected = &retiredClient{
			name:     slot.name,
			revision: version.revision,
			protocol: version.spec.protocol,
			reason:   reason,
			result:   result,
		}
	}
	f.mu.Unlock()

	// 资源发布状态已在锁内确定，日志与外部关闭均在锁外执行。
	if published {
		f.logger.WithContext(ctx).With("event", "client.created", "client", slot.name, "revision", version.revision, "protocol", version.spec.protocol.String()).Info("client created")
	}
	if rejected != nil {
		f.closeAndLog(*rejected)
	}

	f.mu.Lock()
	if slot.build == call {
		slot.build = nil
	}
	close(call.done)
	f.mu.Unlock()
}

func (f *factory) beginActivity() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return ErrFactoryClosed
	}
	f.activities++
	return nil
}

func detachRetired(name string, version *clientVersion) retiredClient {
	retired := retiredClient{
		name:     name,
		revision: version.revision,
		protocol: version.spec.protocol,
		reason:   version.retireReason,
		result:   version.client,
	}
	version.client = clientResult{}
	return retired
}

func (f *factory) closeAndLog(retired retiredClient) {
	logger := f.logger.With(
		"client", retired.name,
		"revision", retired.revision,
		"protocol", retired.protocol.String(),
		"reason", string(retired.reason),
	)
	if err := retired.result.close(); err != nil {
		logger.With("event", "client.close.failed", "error", err).Error("client close failed")
		return
	}
	logger.With("event", "client.closed").Info("client closed")
}

type clientResult struct {
	// httpClient HTTP 客户端；成功构建时与 grpcClient 恰有一个非 nil。
	httpClient *kratoshttp.Client
	// grpcClient gRPC 连接；成功构建时与 httpClient 恰有一个非 nil。
	grpcClient *stdgrpc.ClientConn
	// closeFn 释放本次构建资源的函数。
	closeFn func() error
}

func (r clientResult) validate() error {
	if (r.httpClient == nil) == (r.grpcClient == nil) {
		return ErrInvalidBuildResult
	}
	return nil
}

func (r clientResult) close() error {
	if r.closeFn != nil {
		return r.closeFn()
	}
	return nil
}

func (r clientResult) withCancel(cancel context.CancelFunc) clientResult {
	closeFn := r.closeFn
	r.closeFn = func() error {
		defer cancel()
		if closeFn != nil {
			return closeFn()
		}
		return nil
	}
	return r
}

func (r clientResult) present() bool {
	return r.httpClient != nil || r.grpcClient != nil
}
