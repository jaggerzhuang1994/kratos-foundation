package mysql

import (
	"fmt"
	"os"

	"context"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
)

func TestDriverRegistered(t *testing.T) {
	if !slices.Contains(database.RegisteredDrivers(), DriverName) {
		t.Fatalf("registered drivers = %v", database.RegisteredDrivers())
	}
}

func TestNewConnection(t *testing.T) {
	connection, err := newConnection(database.DriverConfig{
		Name: "primary",
		DSN:  "root:password@tcp(localhost:3306)/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.SQLDB == nil || connection.Dialector == nil {
		t.Fatalf("connection = %#v", connection)
	}
	if err := connection.SQLDB.Close(); err != nil {
		t.Fatal(err)
	}
}

type scriptedConnector struct {
	connect func(context.Context) (driver.Conn, error)
}

func (c *scriptedConnector) Connect(ctx context.Context) (driver.Conn, error) { return c.connect(ctx) }
func (*scriptedConnector) Driver() driver.Driver                              { return &drivermysql.MySQLDriver{} }

type scriptedConnection struct {
	exec   func() (driver.Result, error)
	closed atomic.Int32
}

func (*scriptedConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *scriptedConnection) Close() error { c.closed.Add(1); return nil }
func (*scriptedConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *scriptedConnection) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return c.exec()
}

func TestConnectorRetriesOnlyPhysicalConnectionsWithProgressiveWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts []time.Time
		want := &scriptedConnection{}
		connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
			attempts = append(attempts, time.Now())
			if len(attempts) < 5 {
				return nil, io.EOF
			}
			return want, nil
		}}}
		got, err := connector.Connect(context.Background())
		if err != nil || got != want || len(attempts) != 5 {
			t.Fatalf("Connect = (%v, %v), attempts=%d", got, err, len(attempts))
		}
		for index := 1; index < len(attempts); index++ {
			maximum := 100 * time.Millisecond * time.Duration(1<<(index-1))
			wait := attempts[index].Sub(attempts[index-1])
			if wait < maximum*8/10 || wait > maximum {
				t.Fatalf("wait %d = %s, want [%s, %s]", index, wait, maximum*8/10, maximum)
			}
		}
	})
}

func TestConnectorStopsAfterFiveTransientFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
			attempts++
			return nil, io.ErrUnexpectedEOF
		}}}
		got, err := connector.Connect(context.Background())
		if got != nil || !errors.Is(err, io.ErrUnexpectedEOF) || attempts != 5 {
			t.Fatalf("Connect = (%v, %v), attempts=%d", got, err, attempts)
		}
	})
}

func TestConnectorDoesNotRetryAuthenticationFailure(t *testing.T) {
	attempts := 0
	failure := &drivermysql.MySQLError{Number: 1045, Message: "access denied"}
	connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
		attempts++
		return nil, failure
	}}}
	if _, err := connector.Connect(context.Background()); !errors.Is(err, failure) || attempts != 1 {
		t.Fatalf("error=%v, attempts=%d", err, attempts)
	}
	if _, ok := connector.Driver().(*drivermysql.MySQLDriver); !ok {
		t.Fatalf("Driver = %T", connector.Driver())
	}
}

func TestConnectorCancellationStopsWaitingAndSkipsFurtherAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(context.Context) (driver.Conn, error) {
			attempts++
			go func() { time.Sleep(time.Millisecond); cancel() }()
			return nil, io.EOF
		}}}
		start := time.Now()
		_, err := connector.Connect(ctx)
		if time.Since(start) != time.Millisecond {
			t.Fatalf("cancellation elapsed = %s", time.Since(start))
		}
		if !errors.Is(err, context.Canceled) || attempts != 1 {
			t.Fatalf("error=%v, attempts=%d", err, attempts)
		}
		_, err = connector.Connect(ctx)
		if !errors.Is(err, context.Canceled) || attempts != 1 {
			t.Fatalf("already canceled: error=%v, attempts=%d", err, attempts)
		}
	})
}

func TestConnectorBoundsDialByBudgetAndCallerDeadline(t *testing.T) {
	for _, callerTimeout := range []time.Duration{time.Minute, 20 * time.Millisecond} {
		t.Run(callerTimeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), callerTimeout)
				defer cancel()
				attempts := 0
				connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(ctx context.Context) (driver.Conn, error) {
					attempts++
					<-ctx.Done()
					return nil, ctx.Err()
				}}}
				start := time.Now()
				_, err := connector.Connect(ctx)
				want := min(callerTimeout, 30*time.Second)
				if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 || time.Since(start) != want {
					t.Fatalf("error=%v, attempts=%d, elapsed=%s, want %s", err, attempts, time.Since(start), want)
				}
			})
		})
	}
}

func TestConnectorBudgetIncludesAllAttemptsAndWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		connector := &reconnectingConnector{Connector: &scriptedConnector{connect: func(ctx context.Context) (driver.Conn, error) {
			attempts++
			timer := time.NewTimer(12 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return nil, io.EOF
			}
		}}}
		start := time.Now()
		_, err := connector.Connect(context.Background())
		if !errors.Is(err, context.DeadlineExceeded) || attempts != 3 || time.Since(start) != 30*time.Second {
			t.Fatalf("error=%v, attempts=%d, elapsed=%s", err, attempts, time.Since(start))
		}
	})
}

func TestExternalMySQLRollbackCancellationAndReconnect(t *testing.T) {
	dsn := os.Getenv("FOUNDATION_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FOUNDATION_TEST_MYSQL_DSN for Docker integration tests")
	}
	connection, err := newConnection(database.DriverConfig{Name: "external", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	db := connection.SQLDB
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	table := fmt.Sprintf("foundation_test_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			t.Error(err)
		}
	})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" (id) VALUES (?)", 1); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback rows=%d err=%v", count, err)
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	start := time.Now()
	_, err = db.ExecContext(short, "SELECT SLEEP(3)")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel=%v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("query canceled in %s; pool recovered; stats=%+v", time.Since(start), db.Stats())
}
