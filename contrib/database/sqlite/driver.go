// Package sqlite 注册 GORM SQLite3 数据库驱动。
package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	gormsqlite "gorm.io/driver/sqlite"
)

// DriverName 是配置中使用的 SQLite3 驱动名称。
const DriverName = "sqlite3"

func init() {
	database.MustRegisterDriver(DriverName, newConnection)
}

func newConnection(config database.DriverConfig) (database.DriverConnection, error) {
	db, err := sql.Open(gormsqlite.DriverName, config.DSN)
	if err != nil {
		return database.DriverConnection{}, fmt.Errorf("open SQLite3 connection: %w", err)
	}
	return database.DriverConnection{
		SQLDB: db,
		Dialector: gormsqlite.New(gormsqlite.Config{
			DSN:  config.DSN,
			Conn: db,
		}),
	}, nil
}
