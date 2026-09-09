package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	gormmysql "gorm.io/driver/mysql"
)

func TestExistingPoolRecoversAfterPhysicalConnectionLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var broken atomic.Bool
		first := &scriptedConnection{exec: func() (driver.Result, error) {
			if broken.Load() {
				return nil, driver.ErrBadConn
			}
			return driver.RowsAffected(1), nil
		}}
		recovered := &scriptedConnection{exec: func() (driver.Result, error) { return driver.RowsAffected(1), nil }}
		var attempts atomic.Int32
		connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
			switch attempts.Add(1) {
			case 1:
				return first, nil
			case 2, 3:
				return nil, io.EOF
			default:
				return recovered, nil
			}
		}}}
		pool := sql.OpenDB(connector)
		pool.SetMaxOpenConns(1)
		defer func() {
			if err := pool.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := pool.ExecContext(context.Background(), "UPDATE fixture SET value=1"); err != nil {
			t.Fatal(err)
		}
		broken.Store(true)
		if _, err := pool.ExecContext(context.Background(), "UPDATE fixture SET value=2"); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() != 4 || first.closed.Load() != 1 {
			t.Fatalf("attempts=%d, stale closes=%d", attempts.Load(), first.closed.Load())
		}
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(context.Background(), "UPDATE fixture SET value=3"); err == nil {
			t.Fatal("closed pool accepted a query")
		}
		if attempts.Load() != 4 || recovered.closed.Load() != 1 {
			t.Fatalf("cleanup: attempts=%d, recovered closes=%d", attempts.Load(), recovered.closed.Load())
		}
	})
}

func TestConnectionRetryDoesNotReplayPossiblyExecutedSQL(t *testing.T) {
	var executions, attempts atomic.Int32
	connection := &scriptedConnection{exec: func() (driver.Result, error) {
		executions.Add(1)
		return nil, io.ErrUnexpectedEOF
	}}
	pool := sql.OpenDB(&reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
		attempts.Add(1)
		return connection, nil
	}}})
	defer func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := pool.ExecContext(context.Background(), "INSERT INTO fixture(value) VALUES(1)"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error=%v", err)
	}
	if executions.Load() != 1 || attempts.Load() != 1 {
		t.Fatalf("executions=%d, connects=%d", executions.Load(), attempts.Load())
	}
}

func TestNewConnectionPreservesPoolIdentityAndDSNDialTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const network = "mysql-reconnect-timeout-test"
		var attempts atomic.Int32
		drivermysql.RegisterDialContext(network, func(ctx context.Context, _ string) (net.Conn, error) {
			attempts.Add(1)
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != 10*time.Millisecond {
				t.Errorf("dial deadline = %v, present=%t", deadline, ok)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		defer drivermysql.DeregisterDialContext(network)
		result, err := newConnection(database.DriverConfig{DSN: "user:password@" + network + "(localhost:3306)/app?timeout=10ms"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := result.SQLDB.Close(); err != nil {
				t.Error(err)
			}
		}()
		if result.Dialector.(*gormmysql.Dialector).Conn != result.SQLDB {
			t.Fatal("GORM uses a different pool")
		}
		start := time.Now()
		err = result.SQLDB.PingContext(context.Background())
		if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 5 || time.Since(start) < 1250*time.Millisecond || time.Since(start) > 1550*time.Millisecond {
			t.Fatalf("error=%v, attempts=%d, elapsed=%s", err, attempts.Load(), time.Since(start))
		}
	})
}

func TestNewConnectionRejectsInvalidDSN(t *testing.T) {
	result, err := newConnection(database.DriverConfig{DSN: "invalid-dsn"})
	if err == nil || result.SQLDB != nil || result.Dialector != nil {
		t.Fatalf("result=%v, error=%v", result, err)
	}
}
