// Package mysql 注册 GORM MySQL 数据库驱动。
package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	gormmysql "gorm.io/driver/mysql"
)

// DriverName 是配置中使用的 MySQL 驱动名称。
const DriverName = "mysql"

func init() {
	database.MustRegisterDriver(DriverName, newConnection)
}

func newConnection(config database.DriverConfig) (database.DriverConnection, error) {
	driverConfig, err := drivermysql.ParseDSN(config.DSN)
	if err != nil {
		return database.DriverConnection{}, fmt.Errorf("open MySQL connection: %w", err)
	}
	connector, err := drivermysql.NewConnector(driverConfig)
	if err != nil {
		return database.DriverConnection{}, fmt.Errorf("open MySQL connection: %w", err)
	}
	db := sql.OpenDB(&reconnectingConnector{Connector: connector})
	return database.DriverConnection{
		SQLDB: db,
		Dialector: gormmysql.New(gormmysql.Config{
			DSN:  config.DSN,
			Conn: db,
		}),
	}, nil
}

const (
	connectionAttempts = 5
	connectionBudget   = 30 * time.Second
)

// reconnectingConnector 只重试物理建连，不拦截或重放 SQL 与事务。
// 每次 Connect 使用独立退避次数，成功后后续建连仍从首次尝试开始。
type reconnectingConnector struct {
	// Connector 提供底层 MySQL 建连能力。
	driver.Connector
	// backoff 控制暂时连接故障的重试等待。
	backoff reconnect.Backoff
}

func (c *reconnectingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectionBudget)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		connection, err := c.Connector.Connect(ctx)
		if err == nil {
			return connection, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, errors.Join(err, contextErr)
		}
		if attempt == connectionAttempts-1 || !reconnect.Transient(err) {
			return nil, err
		}
		if waitErr := c.backoff.Wait(ctx, attempt); waitErr != nil {
			return nil, errors.Join(err, waitErr)
		}
	}
}
