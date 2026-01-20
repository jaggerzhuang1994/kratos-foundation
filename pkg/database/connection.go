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

type connectionConfig interface {
	GetDriver() string
	GetDsn() string
	GetMaxIdleConns() int32
	GetMaxOpenConns() int32
	GetConnMaxLifetime() *durationpb.Duration
	GetConnMaxIdleTime() *durationpb.Duration
}

type ConnectionFactory interface {
	Make(conf connectionConfig) (gorm.Dialector, error)
}

func NewDefaultConnection(
	connectionFactory ConnectionFactory,
	config Config,
) (DefaultConnection, error) {
	defaultConnConfig, ok := config.GetConnections()[config.GetDefault()]
	if !ok {
		return nil, errors.New("必须存在 database.default 配置的数据库连接")
	}

	defaultConnection, err := connectionFactory.Make(defaultConnConfig)
	if err != nil {
		return nil, err
	}

	return defaultConnection, nil
}

type connectionFactory struct {
}

func NewConnectionFactory() ConnectionFactory {
	return &connectionFactory{}
}

func (f *connectionFactory) Make(conf connectionConfig) (gorm.Dialector, error) {
	switch conf.GetDriver() {
	default:
		fallthrough
	case "mysql":
		db, err := sql.Open(mysql.DefaultDriverName, conf.GetDsn())
		if err != nil {
			return nil, err
		}
		configConnectionPool(db, conf)
		return mysql.New(mysql.Config{DSN: conf.GetDsn(), Conn: db}), nil
	case "sqlite", "sqlite3":
		db, err := sql.Open(sqlite.DriverName, conf.GetDsn())
		if err != nil {
			return nil, err
		}
		configConnectionPool(db, conf)
		return sqlite.New(sqlite.Config{
			DSN:  conf.GetDsn(),
			Conn: db,
		}), nil
	}
}

// 配置连接池
func configConnectionPool(cp *sql.DB, conf connectionConfig) {
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
