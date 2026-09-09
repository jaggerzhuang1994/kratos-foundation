// Package circuitbreaker 提供 client 私有的熔断中间件组装。
package circuitbreaker

import (
	"fmt"
	"math"
	"time"

	aegiscircuitbreaker "github.com/go-kratos/aegis/circuitbreaker"
	"github.com/go-kratos/aegis/circuitbreaker/sre"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/circuitbreaker"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config 是熔断中间件对应的 protobuf 配置类型。
type Config = *config_pb.Middleware_CircuitBreaker

// Client 根据配置创建客户端熔断中间件；未启用时返回 nil 便于组装层跳过。
func Client(config Config) middleware.Middleware {
	if !config.GetEnable() {
		return nil
	}
	return circuitbreaker.Client(circuitbreaker.WithCircuitBreaker(func() aegiscircuitbreaker.CircuitBreaker {
		return newSREBreaker(config.GetSre())
	}))
}

// newSREBreaker 将可选配置转换为 Aegis SRE 熔断器，缺失字段沿用上游默认值。
func newSREBreaker(config *config_pb.Middleware_CircuitBreaker_SREBreaker) aegiscircuitbreaker.CircuitBreaker {
	var opts []sre.Option

	if config != nil {
		if config.Success != nil {
			opts = append(opts, sre.WithSuccess(config.GetSuccess()))
		}

		if config.Request != nil {
			opts = append(opts, sre.WithRequest(config.GetRequest()))
		}

		if config.Bucket != nil {
			opts = append(opts, sre.WithBucket(int(config.GetBucket())))
		}

		if config.Window != nil {
			opts = append(opts, sre.WithWindow(config.GetWindow().AsDuration()))
		}
	}

	return sre.NewBreaker(opts...)
}

// Validate 在客户端构造和热更新入口检查 SRE 参数，避免首次调用延迟构造时 panic。
func Validate(config Config) error {
	if !config.GetEnable() {
		return nil
	}
	cfg := config.GetSre()
	// 与固定 Aegis v0.2.0 的缺省值一致，缺失字段也要参与组合约束校验。
	window, bucket := 3*time.Second, int32(10)
	if cfg != nil {
		if cfg.Window != nil {
			if err := cfg.Window.CheckValid(); err != nil {
				return fmt.Errorf("sre window: %w", err)
			}
			window = cfg.Window.AsDuration()
			if !proto.Equal(durationpb.New(window), cfg.Window) {
				return fmt.Errorf("sre window exceeds time.Duration range")
			}
		}
		if cfg.Bucket != nil {
			bucket = cfg.GetBucket()
		}
		if cfg.Success != nil {
			success := cfg.GetSuccess()
			// SRE 使用 1/success；零值及极小值会产生 Inf，破坏拒绝概率计算。
			if success <= 0 || success > 1 || math.IsNaN(success) || math.IsInf(1/success, 0) {
				return fmt.Errorf("sre success must be in (0, 1] with a finite reciprocal")
			}
		}
		if cfg.GetRequest() < 0 {
			return fmt.Errorf("sre request must be non-negative")
		}
	}
	if bucket <= 0 || window <= 0 {
		return fmt.Errorf("sre window and bucket must be positive")
	}
	if window/time.Duration(bucket) <= 0 {
		return fmt.Errorf("sre bucket duration must be at least 1ns")
	}
	return nil
}
