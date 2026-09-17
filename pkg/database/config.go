package database

import (
	"errors"
	"fmt"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
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
				Colorful:                  proto.Bool(false), // 日志默认适配非终端采集器，不混入 ANSI 颜色码。
				IgnoreRecordNotFoundError: proto.Bool(true),  // 未查到记录是常见分支，默认不将其写成错误日志。
				ParameterizedQueries:      proto.Bool(false), // 默认保留完整 SQL，由业务显式决定是否隐去参数。
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
	effective := new(config_pb.Database)
	if err := manager.Load("database", effective, defaultConfig()); err != nil {
		return nil, err
	}
	if err := validateDatabaseConfig(effective, drivers); err != nil {
		return nil, err
	}
	return effective, nil
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
