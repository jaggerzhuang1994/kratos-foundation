package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
func (f *factory) AcquireClient(ctx context.Context, name string) (
	httpClient *kratoshttp.Client,
	grpcClient *stdgrpc.ClientConn,
	release func(),
	err error,
) {
	if name == "" {
		return nil, nil, nil, ErrInvalidClientName
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
					spec:     newClientSpec(name, f.config.GetClients()[name], f.config),
				},
			}
			f.slots[name] = slot
		}
		version := slot.current
		if version.client.present() {
			version.references++
			f.leases[version] = name
			f.activities++
			result := version.client
			f.mu.Unlock()
			return result.httpClient,
				result.grpcClient,
				sync.OnceFunc(func() { f.releaseClient(name, version) }),
				nil
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
	f.mu.Lock()
	if buildErr == nil {
		call.cancel = nil
	}
	if !f.closed && slot.current == version && slot.build == call {
		if buildErr == nil {
			version.client = result
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
		if f.closed && version.retired && version.retireReason == retireFactoryClosed {
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

func (f *factory) releaseClient(name string, version *clientVersion) {
	defer f.endActivity()

	var retired *retiredClient
	f.mu.Lock()
	version.references--
	if version.references == 0 {
		delete(f.leases, version)
	}
	if version.retired && version.references == 0 && version.client.present() {
		detached := detachRetired(name, version)
		retired = &detached
	}
	f.mu.Unlock()

	if retired != nil {
		f.closeAndLog(*retired)
	}
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
		logger.With("error", err).Error("client close failed")
		return
	}
	logger.Info("client closed")
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
