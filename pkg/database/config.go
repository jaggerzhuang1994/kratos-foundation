package database

import (
	"errors"
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// defaultConfig 返回不依赖外部环境的数据库默认值。
func defaultConfig() *config_pb.Database {
	defaultGormLoggerLevel := config_pb.GormLogger_SILENT

	return &config_pb.Database{
		Gorm: &config_pb.Gorm{
			Logger: &config_pb.GormLogger{
				Level:                     &defaultGormLoggerLevel,
				SlowThreshold:             durationpb.New(200 * time.Millisecond),
				Colorful:                  proto.Bool(false), // 兼容旧配置；结构化日志不输出 ANSI 颜色码。
				IgnoreRecordNotFoundError: proto.Bool(true),  // 未查到记录是常见分支，默认不将其写成错误日志。
				ParameterizedQueries:      proto.Bool(false), // 环境相关默认值由 loadDefaultConfig 补齐，见 defaultParameterizedQueries。
			},
		},
		Default: proto.String("default"),
	}
}

// loadConfig 合并默认值并在打开任何连接前完成结构校验。
func loadConfig(
	manager config.Manager,
	drivers map[string]DriverFactory,
) (*config_pb.Database, error) {
	template, err := loadDefaultConfig(manager)
	if err != nil {
		return nil, err
	}
	effective := new(config_pb.Database)
	if err := manager.Load("database", effective, template); err != nil {
		return nil, err
	}
	if err := validateDatabaseConfig(effective, drivers); err != nil {
		return nil, err
	}
	return effective, nil
}

// loadDefaultConfig 为启动与池参数订阅构造同样的默认模板，订阅后不再读取全局开关。
func loadDefaultConfig(reader config.Reader) (*config_pb.Database, error) {
	defaults, err := observability.Load(reader)
	if err != nil {
		return nil, err
	}
	// 只为独立模板补齐开关；局部显式 true/false 仍由配置合并覆盖。
	template := defaultConfig()
	template.Tracing = &config_pb.GormTracing{Disable: proto.Bool(defaults.TracingDisabled)}
	template.Gorm.Logger.ParameterizedQueries = proto.Bool(defaultParameterizedQueries())
	return template, nil
}

// validateDatabaseConfig 拒绝 GORM 会静默忽略或曲解的配置值。
func validateDatabaseConfig(
	config *config_pb.Database,
	drivers map[string]DriverFactory,
) error {
	if err := validateConnections(config, drivers); err != nil {
		return err
	}
	if err := validateGORMConfig("GORM", config.GetGorm()); err != nil {
		return err
	}
	for name, connection := range config.GetConnections() {
		gormConfig := mergeGORMConfig(config.GetGorm(), connection.GetGorm())
		if err := validateGORMConfig(
			fmt.Sprintf("database connection %q GORM", name),
			gormConfig,
		); err != nil {
			return err
		}
	}
	metricsConfig := config.GetMetrics()
	if interval := metricsConfig.GetRefreshInterval(); interval != nil && interval.AsDuration() <= 0 {
		return errors.New("validate database config: metrics refresh interval must be positive")
	}
	return nil
}

// validateGORMConfig 校验全局或连接合并后的 GORM 数值配置。
func validateGORMConfig(location string, gormConfig *config_pb.Gorm) error {
	if timeout := gormConfig.GetDefaultTransactionTimeout(); timeout != nil && timeout.AsDuration() < 0 {
		return fmt.Errorf("validate database config: %s default transaction timeout must not be negative", location)
	}
	if timeout := gormConfig.GetDefaultContextTimeout(); timeout != nil && timeout.AsDuration() < 0 {
		return fmt.Errorf("validate database config: %s default context timeout must not be negative", location)
	}
	if threshold := gormConfig.GetLogger().GetSlowThreshold(); threshold != nil && threshold.AsDuration() < 0 {
		return fmt.Errorf("validate database config: %s slow threshold must not be negative", location)
	}
	if gormConfig.GetCreateBatchSize() < 0 {
		return fmt.Errorf("validate database config: %s create batch size must not be negative", location)
	}
	return nil
}

// defaultParameterizedQueries 决定 SQL 日志是否默认隐去参数值：与错误堆栈同一条诊断出口约束，
// prod 与未开调试的 pre 隐去参数（避免令牌、密码与个人信息进入日志），local/dev/test 保留完整 SQL 便于排查。
// 显式配置 database.gorm.logger.parameterized_queries 始终优先。
func defaultParameterizedQueries() bool {
	return !env.ExposeDiagnostics()
}
