package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// ReadinessCheck 声明关键依赖的就绪检查。
type ReadinessCheck struct {
	// Name 检查名称，不能为空且不得重复，用于失败定位。
	Name string
	// Check 依赖检查回调；须响应取消并支持并发探针调用。
	Check func(context.Context) error
}

// healthConfig 配置默认健康端点；路径独立于业务 PathPrefix，属于保留路径。
type healthConfig struct {
	// Disable 是否禁用默认健康端点，禁用后将路径交还业务路由。
	Disable bool
	// Addr 独立健康监听地址；留空时仅在业务 HTTP 启用后复用，同址亦复用。
	Addr string
	// LivenessPath 存活探针路径，默认 /healthz。
	LivenessPath string
	// ReadinessPath 就绪探针路径，默认 /readyz。
	ReadinessPath string
	// Timeout 一轮就绪检查的 Context 超时，零值使用一秒；无法强制中断不响应取消的 Check。
	Timeout time.Duration
	// Checks 顺序执行的依赖检查列表。
	Checks []ReadinessCheck
}

type healthState struct {
	// config 构造时复制并补齐默认值的健康检查配置。
	config healthConfig
	// applicationReady 应用就绪判断，启动 HTTP 前绑定。
	applicationReady func() bool
	// stopped 是否进入停机阶段，用于拒绝就绪探针。
	stopped atomic.Bool
	// lastStatus 最近一次就绪状态码，用于只在变化时记录日志。
	lastStatus atomic.Int32
}

func newHealthState(config healthConfig) *healthState {
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
				log.WithModule("server/health").With("event", "readiness.changed", "status", status).Info("service is ready to accept requests")
			} else {
				log.WithModule("server/health").With(
					"event", "readiness.changed",
					"status", status,
					"check", failedCheck,
				).Warn("service is not ready to accept requests")
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
