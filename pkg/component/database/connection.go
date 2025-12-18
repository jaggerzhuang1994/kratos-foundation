package database

import (
	"database/sql"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type DefaultConnection gorm.Dialector

func NewDefaultConnection(cfg *Config) (DefaultConnection, func(), error) {
	var defaultConnection gorm.Dialector
	var rc func()
	defaultConnName := cfg.GetDefault()
	for name, connCfg := range cfg.GetConnections() {
		// 如果名称为 defaultConnName，则为默认连接
		if name == defaultConnName {
			var err error
			defaultConnection, rc, err = newConnection(connCfg)
			if err != nil {
				return nil, nil, err
			}
			break
		}
	}
	if defaultConnection == nil {
		return nil, nil, errors.New("必须存在 database.default 配置的数据库连接")
	}
	return defaultConnection, rc, nil
}

func newConnection(conf interface {
	GetDriver() string
	GetDsn() string
	GetMaxIdleConns() int32
	GetMaxOpenConns() int32
	GetConnMaxLifetime() *durationpb.Duration
	GetConnMaxIdleTime() *durationpb.Duration
}) (gorm.Dialector, func(), error) {
	switch conf.GetDriver() {
	default:
		fallthrough
	case "mysql":
		cp, err := sql.Open(mysql.DefaultDriverName, conf.GetDsn())
		if err != nil {
			return nil, nil, err
		}
		configCp(cp, conf)
		return mysql.New(mysql.Config{DSN: conf.GetDsn(), Conn: cp}), func() {
			_ = cp.Close()
		}, nil
	case "sqlite", "sqlite3":
		cp, err := sql.Open(sqlite.DriverName, conf.GetDsn())
		if err != nil {
			return nil, nil, err
		}
		configCp(cp, conf)
		return sqlite.New(sqlite.Config{
				DSN:  conf.GetDsn(),
				Conn: cp,
			}), func() {
				_ = cp.Close()
			}, nil
	}
}

func configCp(cp *sql.DB, conf interface {
	GetMaxIdleConns() int32
	GetMaxOpenConns() int32
	GetConnMaxLifetime() *durationpb.Duration
	GetConnMaxIdleTime() *durationpb.Duration
}) {
	if conf.GetMaxIdleConns() > 0 {
		cp.SetMaxIdleConns(int(conf.GetMaxIdleConns()))
	}
	if conf.GetMaxOpenConns() > 0 {
		cp.SetMaxOpenConns(int(conf.GetMaxOpenConns()))
	}
	if conf.GetConnMaxLifetime() != nil {
		cp.SetConnMaxLifetime(conf.GetConnMaxLifetime().AsDuration())
	}
	if conf.GetConnMaxIdleTime() != nil {
		cp.SetConnMaxIdleTime(conf.GetConnMaxIdleTime().AsDuration())
	}
}
