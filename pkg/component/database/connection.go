package database

import (
	"github.com/pkg/errors"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type DefaultConnection gorm.Dialector

func NewDefaultConnection(cfg *Config) (DefaultConnection, error) {
	var defaultConnection gorm.Dialector
	defaultConnName := cfg.GetDefault()
	for name, connCfg := range cfg.GetConnections() {
		// 如果名称为 defaultConnName，则为默认连接
		if name == defaultConnName {
			defaultConnection = newConnection(connCfg)
			break
		}
	}
	if defaultConnection == nil {
		return nil, errors.New("必须存在 database.default 配置的数据库连接")
	}
	return defaultConnection, nil
}

func newConnection(conf interface {
	GetDriver() string
	GetDsn() string
}) gorm.Dialector {
	switch conf.GetDriver() {
	default:
		fallthrough
	case "mysql":
		return mysql.New(mysql.Config{DSN: conf.GetDsn()})
	case "sqlite", "sqlite3":
		return sqlite.Open(conf.GetDsn())
	}
}
