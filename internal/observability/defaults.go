// Package observability 解析多个领域共用的观测默认值。
package observability

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
)

// Defaults 是组件构造期读取的独立默认值快照。
type Defaults struct {
	// TracingDisabled 与全局 tracing.disable 的有效值保持一致。
	TracingDisabled bool
}

// Load 读取全局追踪开关；省略时沿用 Tracing Provider 的环境默认值。
// 调用方持有返回快照，不订阅全局开关，避免在运行期改变观测资源生命周期。
func Load(reader config.Reader) (Defaults, error) {
	defaultDisabled := env.IsLocal()
	var defaults Defaults
	if err := reader.Load("tracing.disable", &defaults.TracingDisabled, &defaultDisabled); err != nil {
		return Defaults{}, fmt.Errorf("load tracing.disable: %w", err)
	}
	return defaults, nil
}
