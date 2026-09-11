package database

import (
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newGORMConfig(conf *config_pb.Gorm, gormLogger logger.Interface) *gorm.Config {
	result := &gorm.Config{}
	if conf.GetSkipDefaultTransaction() {
		result.SkipDefaultTransaction = conf.GetSkipDefaultTransaction()
	}
	if conf.GetDefaultTransactionTimeout() != nil {
		result.DefaultTransactionTimeout = conf.GetDefaultTransactionTimeout().AsDuration()
	}
	if conf.GetDefaultContextTimeout() != nil {
		result.DefaultContextTimeout = conf.GetDefaultContextTimeout().AsDuration()
	}
	if conf.GetFullSaveAssociations() {
		result.FullSaveAssociations = conf.GetFullSaveAssociations()
	}
	if conf.GetDisableAutomaticPing() {
		result.DisableAutomaticPing = conf.GetDisableAutomaticPing()
	}
	if conf.GetDisableForeignKeyConstraintWhenMigrating() {
		result.DisableForeignKeyConstraintWhenMigrating = conf.GetDisableForeignKeyConstraintWhenMigrating()
	}
	if conf.GetIgnoreRelationshipsWhenMigrating() {
		result.IgnoreRelationshipsWhenMigrating = conf.GetIgnoreRelationshipsWhenMigrating()
	}
	if conf.GetDisableNestedTransaction() {
		result.DisableNestedTransaction = conf.GetDisableNestedTransaction()
	}
	if conf.GetAllowGlobalUpdate() {
		result.AllowGlobalUpdate = conf.GetAllowGlobalUpdate()
	}
	if conf.GetQueryFields() {
		result.QueryFields = conf.GetQueryFields()
	}
	if conf.GetCreateBatchSize() != 0 {
		result.CreateBatchSize = int(conf.GetCreateBatchSize())
	}
	if conf.GetTranslateError() {
		result.TranslateError = conf.GetTranslateError()
	}
	if conf.GetPropagateUnscoped() {
		result.PropagateUnscoped = conf.GetPropagateUnscoped()
	}
	result.Logger = gormLogger
	return result
}

// MergeGORMConfig applies explicitly configured connection fields over global fields.
func mergeGORMConfig(base, override *config_pb.Gorm) *config_pb.Gorm {
	result := new(config_pb.Gorm)
	if base != nil {
		result = proto.Clone(base).(*config_pb.Gorm)
	}
	if override != nil {
		proto.Merge(result, override)
		// Duration 表示一个完整阈值，不能把覆盖值的秒/纳秒分别与全局值合并。
		// 显式 0s 也必须替换全局值，使当前连接可以关闭慢操作判断。
		if threshold := override.GetLogger().GetSlowThreshold(); threshold != nil {
			result.Logger.SlowThreshold = proto.Clone(threshold).(*durationpb.Duration)
		}
	}
	return result
}

type gormLoggerWriter struct {
	logger log.Logger
}

func newGORMLogger(log log.Logger, config *config_pb.GormLogger) logger.Interface {
	level := logger.Silent
	switch config.GetLevel() {
	case config_pb.GormLogger_INFO:
		level = logger.Info
	case config_pb.GormLogger_WARN:
		level = logger.Warn
	case config_pb.GormLogger_ERROR:
		level = logger.Error
	}

	return logger.New(&gormLoggerWriter{
		logger: log.AddCallerDepth(),
	}, logger.Config{
		SlowThreshold:             config.GetSlowThreshold().AsDuration(),
		Colorful:                  config.GetColorful(),
		IgnoreRecordNotFoundError: config.GetIgnoreRecordNotFoundError(),
		ParameterizedQueries:      config.GetParameterizedQueries(),
		LogLevel:                  level,
	})
}

func (writer *gormLoggerWriter) Printf(format string, arguments ...any) {
	format = strings.ReplaceAll(format, "\n", " ")
	message := fmt.Sprintf(format, arguments...)
	switch {
	case strings.Contains(format, "[error]") || secondGORMLogArgumentIsError(arguments):
		writer.logger.Error(message)
	case strings.Contains(format, "[warn]") || secondGORMLogArgumentIsSlowSQL(arguments):
		writer.logger.Warn(message)
	default:
		writer.logger.Info(message)
	}
}

func secondGORMLogArgumentIsError(arguments []any) bool {
	if len(arguments) < 2 {
		return false
	}
	_, ok := arguments[1].(error)
	return ok
}

func secondGORMLogArgumentIsSlowSQL(arguments []any) bool {
	if len(arguments) < 2 {
		return false
	}
	message, ok := arguments[1].(string)
	return ok && strings.HasPrefix(message, "SLOW SQL >=")
}
