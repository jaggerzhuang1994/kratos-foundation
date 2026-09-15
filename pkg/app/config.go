package app

import (
	"fmt"
	"time"

	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type Config = *config_pb.App

// defaultConfig 只作为合并模板使用，Manager.Load 会先复制，因此这里可以安全复用。
var defaultConfig = &config_pb.App{
	RegistrarTimeout: durationpb.New(10 * time.Second),
	StopTimeout:      durationpb.New(30 * time.Second),
	Endpoints:        nil,
	Metadata:         nil,
}

// NewConfig 在应用组装阶段读取一次配置。运行期只有明确声明可热更新的策略才订阅
// Manager，避免应用身份等启动参数在运行中出现部分更新。
func NewConfig(manager foundationconfig.Manager) (Config, error) {
	next := new(config_pb.App)
	if err := manager.Load("app", next, defaultConfig); err != nil {
		return nil, err
	}
	if next.Registry == "" {
		next.Registry = "default"
	}
	if err := validateConfig(next); err != nil {
		return nil, err
	}
	return next, nil
}

// validateConfig 补充 protobuf 规则之外的正时长约束。
func validateConfig(config Config) error {
	if err := config.ValidateAll(); err != nil {
		return fmt.Errorf("validate app config: %w", err)
	}
	registrarTimeout := config.GetRegistrarTimeout()
	if err := registrarTimeout.CheckValid(); err != nil {
		return fmt.Errorf("app registrar_timeout is invalid: %w", err)
	}
	if timeout := registrarTimeout.AsDuration(); timeout <= 0 {
		return fmt.Errorf("app registrar_timeout must be positive")
	}
	stopTimeout := config.GetStopTimeout()
	if err := stopTimeout.CheckValid(); err != nil {
		return fmt.Errorf("app stop_timeout is invalid: %w", err)
	}
	if timeout := stopTimeout.AsDuration(); timeout <= 0 {
		return fmt.Errorf("app stop_timeout must be positive")
	}
	return nil
}
