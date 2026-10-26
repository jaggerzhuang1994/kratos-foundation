package server

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// grpcLivenessService 是 gRPC 健康检查中只表示进程存活的服务名，对应 HTTP 的 liveness 路径。
const grpcLivenessService = "liveness"

// grpcHealthWatchInterval 是 Watch 重新评估就绪状态的间隔。
const grpcHealthWatchInterval = time.Second

// grpcHealth 让 gRPC 健康协议与 HTTP 探针共用同一就绪状态，替代 Kratos 在 Start 后即报 SERVING 的内置实现。
type grpcHealth struct {
	healthpb.UnimplementedHealthServer
	// state 与 HTTP 探针共享的应用就绪、停机与依赖检查状态。
	state *healthState
	// services 返回当前已注册的 gRPC 服务名，用于识别未知服务。
	services func() map[string]grpc.ServiceInfo
}

// Check 与 HTTP readiness 一致：应用就绪、未停机且依赖检查通过才返回 SERVING；liveness 恒为 SERVING。
func (h *grpcHealth) Check(ctx context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if err := h.known(request.GetService()); err != nil {
		return nil, err
	}
	serving := request.GetService() == grpcLivenessService || h.state.ready(ctx)
	return &healthpb.HealthCheckResponse{Status: servingStatus(serving)}, nil
}

// Watch 立即推送当前状态，之后按固定间隔重新评估并只在变化时推送，直到客户端断开。
// 它只反映应用就绪与停机，不执行依赖检查，避免每个订阅方周期性触发依赖探测。
func (h *grpcHealth) Watch(request *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	if err := h.known(request.GetService()); err != nil {
		return err
	}
	ticker := time.NewTicker(grpcHealthWatchInterval)
	defer ticker.Stop()
	last := healthpb.HealthCheckResponse_UNKNOWN
	for {
		serving := request.GetService() == grpcLivenessService ||
			!h.state.stopped.Load() && h.state.applicationReady != nil && h.state.applicationReady()
		if current := servingStatus(serving); current != last {
			if err := stream.Send(&healthpb.HealthCheckResponse{Status: current}); err != nil {
				return err
			}
			last = current
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
		}
	}
}

// known 按健康协议对未注册的服务名返回 NotFound；空服务名表示整个服务器。
func (h *grpcHealth) known(service string) error {
	if service == "" || service == grpcLivenessService {
		return nil
	}
	if _, ok := h.services()[service]; ok {
		return nil
	}
	return status.Errorf(codes.NotFound, "unknown service %q", service)
}

func servingStatus(serving bool) healthpb.HealthCheckResponse_ServingStatus {
	if serving {
		return healthpb.HealthCheckResponse_SERVING
	}
	return healthpb.HealthCheckResponse_NOT_SERVING
}
