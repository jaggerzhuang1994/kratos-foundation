package consul

import (
	"context"
	"errors"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
)

// heartbeat 只重试健康续报和缺失登记，不重放业务请求，也不隐式注销服务。
func (r *registrar) heartbeat(ctx context.Context, payload *api.AgentServiceRegistration) {
	backoff := reconnect.Backoff{}
	attempt := 0
	for {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		err := r.client.Agent().UpdateTTLOpts("service:"+payload.ID, "pass", "pass", (&api.QueryOptions{}).WithContext(requestCtx))
		cancel()
		if ctx.Err() != nil {
			return
		}
		var status api.StatusError
		if errors.As(err, &status) && status.Code == 404 {
			// Agent 重启或 TTL 被自动清理后，仅重建本实例的同一登记快照。
			requestCtx, cancel = context.WithTimeout(ctx, requestTimeout)
			err = r.client.Agent().ServiceRegisterOpts(payload, api.ServiceRegisterOpts{}.WithContext(requestCtx))
			cancel()
			if err == nil {
				// Consul 新建 TTL check 为 critical；下一轮优先续报，只等待失败退避而非完整健康间隔。
				if err = backoff.Wait(ctx, attempt); err != nil {
					return
				}
				attempt++
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if !retryable(err) {
				r.logger.With("service.id", payload.ID, "attempt", attempt+1, "error", err).Error("consul heartbeat stopped after a non-retryable failure")
				return
			}
			r.logger.With("service.id", payload.ID, "attempt", attempt+1, "error", err).Warn("consul heartbeat failed; retrying")
			if err = backoff.Wait(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		if attempt > 0 {
			r.logger.With("service.id", payload.ID, "attempts", attempt).Info("consul heartbeat recovered")
		}
		attempt = 0
		timer := time.NewTimer(time.Duration(r.config.healthCheckIntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func retryable(err error) bool {
	var status api.StatusError
	if errors.As(err, &status) {
		return status.Code == 429 || status.Code >= 500 && status.Code < 600
	}
	return reconnect.Transient(err)
}
