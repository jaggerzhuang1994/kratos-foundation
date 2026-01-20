package database

import (
	"gorm.io/gorm"
)

type GormConfig gorm.Option

func NewGormConfig(config Config, logger GormLogger) GormConfig {
	gormConfig := &gorm.Config{}
	conf := config.GetGorm()

	// SkipDefaultTransaction
	if conf.GetSkipDefaultTransaction() {
		gormConfig.SkipDefaultTransaction = conf.GetSkipDefaultTransaction()
	}

	// DefaultTransactionTimeout
	if conf.GetDefaultTransactionTimeout() != nil {
		gormConfig.DefaultTransactionTimeout = conf.GetDefaultTransactionTimeout().AsDuration()
	}

	// DefaultContextTimeout
	if conf.GetDefaultContextTimeout() != nil {
		gormConfig.DefaultContextTimeout = conf.GetDefaultContextTimeout().AsDuration()
	}

	// FullSaveAssociations
	if conf.GetFullSaveAssociations() {
		gormConfig.FullSaveAssociations = conf.GetFullSaveAssociations()
	}

	// DisableAutomaticPing
	if conf.GetDisableAutomaticPing() {
		gormConfig.DisableAutomaticPing = conf.GetDisableAutomaticPing()
	}

	// DisableForeignKeyConstraintWhenMigrating
	if conf.GetDisableForeignKeyConstraintWhenMigrating() {
		gormConfig.DisableForeignKeyConstraintWhenMigrating = conf.GetDisableForeignKeyConstraintWhenMigrating()
	}

	// IgnoreRelationshipsWhenMigrating
	if conf.GetIgnoreRelationshipsWhenMigrating() {
		gormConfig.IgnoreRelationshipsWhenMigrating = conf.GetIgnoreRelationshipsWhenMigrating()
	}

	// DisableNestedTransaction
	if conf.GetDisableNestedTransaction() {
		gormConfig.DisableNestedTransaction = conf.GetDisableNestedTransaction()
	}

	// AllowGlobalUpdate
	if conf.GetAllowGlobalUpdate() {
		gormConfig.AllowGlobalUpdate = conf.GetAllowGlobalUpdate()
	}

	// QueryFields
	if conf.GetQueryFields() {
		gormConfig.QueryFields = conf.GetQueryFields()
	}

	// CreateBatchSize
	if conf.GetCreateBatchSize() != 0 {
		gormConfig.CreateBatchSize = int(conf.GetCreateBatchSize())
	}

	// TranslateError
	if conf.GetTranslateError() {
		gormConfig.TranslateError = conf.GetTranslateError()
	}

	// PropagateUnscoped
	if conf.GetPropagateUnscoped() {
		gormConfig.PropagateUnscoped = conf.GetPropagateUnscoped()
	}

	if logger != nil {
		gormConfig.Logger = logger
	}

	return gormConfig
}
