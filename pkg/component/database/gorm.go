package database

import (
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/log"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type gormLoggerWriter struct {
	*log.Helper
}

const logModule = "gorm"

func NewGormLogger(log *log.Log, cfg *Config) logger.Interface {
	gormLogger := cfg.GetGorm().GetLogger()
	if gormLogger == nil {
		gormLogger = defaultGormLogger
	}

	var level = logger.Silent
	switch gormLogger.GetLevel() {
	case kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm_Logger_Info:
		level = logger.Info
	case kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm_Logger_Warn:
		level = logger.Warn
	case kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm_Logger_Error:
		level = logger.Error
	}

	return logger.New(&gormLoggerWriter{
		log.WithModule(logModule, cfg.GetLog()).NewHelper(),
	}, logger.Config{
		SlowThreshold:             gormLogger.GetSlowThreshold().AsDuration(),
		Colorful:                  gormLogger.GetColorful(),
		IgnoreRecordNotFoundError: gormLogger.GetIgnoreRecordNotFoundError(),
		ParameterizedQueries:      gormLogger.GetParameterizedQueries(),
		LogLevel:                  level,
	})
}

func (w *gormLoggerWriter) Printf(s string, i ...any) {
	s = strings.Replace(s, "%s\n", "%s", -1)
	w.Debugf(s, i...)
}

func NewGormConfig(cfg *kratos_foundation_pb.DatabaseComponentConfig_Database_Gorm, logger logger.Interface) *gorm.Config {
	gormConfig := &gorm.Config{}

	// SkipDefaultTransaction
	if cfg.GetSkipDefaultTransaction() {
		gormConfig.SkipDefaultTransaction = cfg.GetSkipDefaultTransaction()
	}

	// DefaultTransactionTimeout
	if cfg.GetDefaultTransactionTimeout() != nil {
		gormConfig.DefaultTransactionTimeout = cfg.GetDefaultTransactionTimeout().AsDuration()
	}

	// DefaultContextTimeout
	if cfg.GetDefaultContextTimeout() != nil {
		gormConfig.DefaultContextTimeout = cfg.GetDefaultContextTimeout().AsDuration()
	}

	// FullSaveAssociations
	if cfg.GetFullSaveAssociations() {
		gormConfig.FullSaveAssociations = cfg.GetFullSaveAssociations()
	}

	// DisableAutomaticPing
	if cfg.GetDisableAutomaticPing() {
		gormConfig.DisableAutomaticPing = cfg.GetDisableAutomaticPing()
	}

	// DisableForeignKeyConstraintWhenMigrating
	if cfg.GetDisableForeignKeyConstraintWhenMigrating() {
		gormConfig.DisableForeignKeyConstraintWhenMigrating = cfg.GetDisableForeignKeyConstraintWhenMigrating()
	}

	// IgnoreRelationshipsWhenMigrating
	if cfg.GetIgnoreRelationshipsWhenMigrating() {
		gormConfig.IgnoreRelationshipsWhenMigrating = cfg.GetIgnoreRelationshipsWhenMigrating()
	}

	// DisableNestedTransaction
	if cfg.GetDisableNestedTransaction() {
		gormConfig.DisableNestedTransaction = cfg.GetDisableNestedTransaction()
	}

	// AllowGlobalUpdate
	if cfg.GetAllowGlobalUpdate() {
		gormConfig.AllowGlobalUpdate = cfg.GetAllowGlobalUpdate()
	}

	// QueryFields
	if cfg.GetQueryFields() {
		gormConfig.QueryFields = cfg.GetQueryFields()
	}

	// CreateBatchSize
	if cfg.GetCreateBatchSize() != 0 {
		gormConfig.CreateBatchSize = int(cfg.GetCreateBatchSize())
	}

	// TranslateError
	if cfg.GetTranslateError() {
		gormConfig.TranslateError = cfg.GetTranslateError()
	}

	// PropagateUnscoped
	if cfg.GetPropagateUnscoped() {
		gormConfig.PropagateUnscoped = cfg.GetPropagateUnscoped()
	}

	if logger != nil {
		gormConfig.Logger = logger
	}

	return gormConfig
}
