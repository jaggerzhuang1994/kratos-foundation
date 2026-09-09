// Package ratelimit 提供 server 私有的限流中间件组装。
package ratelimit

import (
	"fmt"
	"math"
	"time"

	aegisratelimit "github.com/go-kratos/aegis/ratelimit"
	"github.com/go-kratos/aegis/ratelimit/bbr"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/ratelimit"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config 是限流中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_RateLimit

// Server 根据配置创建服务端自适应限流中间件；未启用时返回 nil。
func Server(config Config) middleware.Middleware {
	if !config.GetEnable() {
		return nil
	}
	return ratelimit.Server(ratelimit.WithLimiter(newBBRLimiter(config.GetBbrLimiter())))
}

// newBBRLimiter 将可选配置转换为 Aegis BBR 限流器，缺失字段沿用上游默认值。
func newBBRLimiter(bbrCfg *config_pb.Middleware_RateLimit_BBRLimiter) aegisratelimit.Limiter {
	var opts []bbr.Option

	if bbrCfg != nil {
		if bbrCfg.Window != nil {
			opts = append(opts, bbr.WithWindow(bbrCfg.GetWindow().AsDuration()))
		}

		if bbrCfg.Bucket != nil {
			opts = append(opts, bbr.WithBucket(int(bbrCfg.GetBucket())))
		}

		if bbrCfg.CpuThreshold != nil {
			opts = append(opts, bbr.WithCPUThreshold(bbrCfg.GetCpuThreshold()))
		}

		if bbrCfg.CpuQuota != nil {
			opts = append(opts, bbr.WithCPUQuota(bbrCfg.GetCpuQuota()))
		}
	}

	return bbr.NewLimiter(opts...)
}

// Validate 在构造和热更新入口检查启用的 BBR 参数，避免 Aegis 在请求期发生除零。
func Validate(config Config) error {
	if !config.GetEnable() {
		return nil
	}
	cfg := config.GetBbrLimiter()
	// 与固定 Aegis v0.2.0 的缺省值一致，缺失字段也要参与组合约束校验。
	window, bucket := 10*time.Second, int32(100)
	if cfg != nil {
		if cfg.Window != nil {
			if err := cfg.Window.CheckValid(); err != nil {
				return fmt.Errorf("bbr window: %w", err)
			}
			window = cfg.Window.AsDuration()
			if !proto.Equal(durationpb.New(window), cfg.Window) {
				return fmt.Errorf("bbr window exceeds time.Duration range")
			}
		}
		if cfg.Bucket != nil {
			bucket = cfg.GetBucket()
		}
		if cfg.CpuThreshold != nil && cfg.GetCpuThreshold() <= 0 {
			return fmt.Errorf("bbr cpu_threshold must be positive")
		}
		// quota=0 保留 Aegis 的默认 CPU 采样；非零配额必须为有限正数。
		if quota := cfg.GetCpuQuota(); quota < 0 || math.IsNaN(quota) || math.IsInf(quota, 0) {
			return fmt.Errorf("bbr cpu_quota must be finite and non-negative")
		}
	}
	if bucket <= 0 || window <= 0 {
		return fmt.Errorf("bbr window and bucket must be positive")
	}
	// BBR 同时计算 window/bucket 与 1s/桶时长，两者均需非零。
	if bucketDuration := window / time.Duration(bucket); bucketDuration <= 0 || bucketDuration > time.Second {
		return fmt.Errorf("bbr bucket duration must be between 1ns and 1s")
	}
	return nil
}
