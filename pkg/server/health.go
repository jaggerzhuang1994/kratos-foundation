package server

import (
	"context"
	"fmt"
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ReadinessCheck 声明关键依赖检查。Check 必须响应 Context，且可以被多个探针并发调用。
type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

// HealthConfig 配置默认健康端点。零值启用 /healthz、/readyz，检查总超时为一秒。
// 路径独立于业务 PathPrefix，属于保留路径；Disable 可将路径交还业务路由。
type HealthConfig struct {
	Disable       bool
	Addr          string
	LivenessPath  string
	ReadinessPath string
	Timeout       time.Duration
	Checks        []ReadinessCheck
}

type healthState struct {
	config           HealthConfig
	applicationReady func() bool
	stopped          atomic.Bool
	lastStatus       atomic.Int32
}

func newHealthState(config HealthConfig) *healthState {
	if config.LivenessPath == "" {
		config.LivenessPath = "/healthz"
	}
	if config.ReadinessPath == "" {
		config.ReadinessPath = "/readyz"
	}
	if config.Timeout == 0 {
		config.Timeout = time.Second
	}
	config.Checks = append([]ReadinessCheck(nil), config.Checks...)
	return &healthState{config: config}
}

func (h *healthState) validate(metricsPath string) error {
	if h.config.Disable {
		return nil
	}
	for _, path := range []string{h.config.LivenessPath, h.config.ReadinessPath} {
		parsed, err := url.ParseRequestURI(path)
		if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.TrimSpace(path) != path || parsed.Path != path || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("invalid health path %q", path)
		}
		if path == metricsPath {
			return fmt.Errorf("health path %q conflicts with metrics", path)
		}
	}
	if h.config.LivenessPath == h.config.ReadinessPath {
		return fmt.Errorf("health paths must be distinct")
	}
	if h.config.Timeout <= 0 {
		return fmt.Errorf("health check timeout must be positive")
	}
	names := make(map[string]bool)
	for _, check := range h.config.Checks {
		if strings.TrimSpace(check.Name) == "" || check.Check == nil || names[check.Name] {
			return fmt.Errorf("invalid or duplicate readiness check %q", check.Name)
		}
		names[check.Name] = true
	}
	return nil
}

// wrap 在业务 Filter、鉴权和限流之前处理保留路径，避免探针被业务策略误伤。
func (h *healthState) wrap(next http.Handler) http.Handler {
	if h.config.Disable {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		live, ready := r.URL.Path == h.config.LivenessPath, r.URL.Path == h.config.ReadinessPath
		if !live && !ready {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status := http.StatusOK
		if ready && !h.ready(r.Context()) {
			status = http.StatusServiceUnavailable
		}
		// 只公开状态码，不泄漏依赖错误、地址或配置内容。
		w.WriteHeader(status)
	})
}

func (h *healthState) ready(ctx context.Context) (ready bool) {
	failedCheck := "application"
	defer func() {
		status := int32(http.StatusServiceUnavailable)
		if ready {
			status = http.StatusOK
		}
		// 仅记录探针结果变化，不在每次请求上写日志；检查名由组装层提供，不记录原始错误。
		if h.lastStatus.Swap(status) != status {
			if ready {
				kratoslog.Infow("event", "healthState.ready | readiness.changed", "status", status)
			} else {
				kratoslog.Warnw("event", "healthState.ready | readiness.changed", "status", status, "check", failedCheck)
			}
		}
	}()
	if h.stopped.Load() || h.applicationReady == nil || !h.applicationReady() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, h.config.Timeout)
	defer cancel()
	for _, check := range h.config.Checks {
		failedCheck = check.Name
		if ctx.Err() != nil || check.Check(ctx) != nil {
			return false
		}
	}
	// 依赖检查期间可能收到停机请求；返回前再次检查。
	return ctx.Err() == nil && !h.stopped.Load() && h.applicationReady()
}

// SetReadinessSource 在组装阶段绑定应用就绪状态，必须在启动 HTTP 前调用。
// NewServerBootstrap 自动绑定；手工组装应提供完整启动且未停机的判断函数。
func (r *Runtime) SetReadinessSource(ready func() bool) {
	r.health.applicationReady = ready
}
