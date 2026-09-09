package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	gormsqlite "gorm.io/driver/sqlite"
)

func TestConnectionFactoryUsesRegisteredDriver(t *testing.T) {
	called := false
	factory := newConnectionFactory(map[string]DriverFactory{
		"fake": func(config DriverConfig) (DriverConnection, error) {
			called = true
			if config.Name != "primary" || config.DSN != "fixture-dsn" {
				t.Fatalf("driver config = %#v", config)
			}
			db, err := sql.Open(gormsqlite.DriverName, ":memory:")
			if err != nil {
				return DriverConnection{}, err
			}
			return DriverConnection{
				SQLDB: db,
				Dialector: gormsqlite.New(gormsqlite.Config{
					DSN:  ":memory:",
					Conn: db,
				}),
			}, nil
		},
	})
	t.Cleanup(func() {
		if err := factory.close(); err != nil {
			t.Error(err)
		}
	})

	dialector, err := factory.make("primary", &config_pb.DBConnection{
		Driver: proto.String(" FAKE "),
		Dsn:    "fixture-dsn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || dialector == nil {
		t.Fatalf("called = %t, dialector = %#v", called, dialector)
	}
}

func TestConnectionFactoryTracksPoolsInStableOrderAndClosesIdempotently(t *testing.T) {
	factory := newConnectionFactory(map[string]DriverFactory{
		"fake": func(config DriverConfig) (DriverConnection, error) {
			db, err := sql.Open(gormsqlite.DriverName, ":memory:")
			if err != nil {
				return DriverConnection{}, err
			}
			return DriverConnection{SQLDB: db, Dialector: gormsqlite.New(gormsqlite.Config{Conn: db})}, nil
		},
	})
	for _, name := range []string{"zeta", "alpha", "middle"} {
		if _, err := factory.make(name, &config_pb.DBConnection{Driver: proto.String("fake")}); err != nil {
			t.Fatal(err)
		}
	}
	pools := factory.pools()
	if got := []string{pools[0].name, pools[1].name, pools[2].name}; strings.Join(got, ",") != "alpha,middle,zeta" {
		t.Fatalf("pools=%v", got)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := factory.close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})
	}
	wg.Wait()
	if err := factory.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.make("later", &config_pb.DBConnection{Driver: proto.String("fake")}); !errors.Is(err, errConnectionFactoryClosed) {
		t.Fatalf("error=%v", err)
	}
}

func TestConnectionFactoryRollsBackPartialDriverFailure(t *testing.T) {
	db, err := sql.Open(gormsqlite.DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(map[string]DriverFactory{"fake": func(DriverConfig) (DriverConnection, error) {
		return DriverConnection{SQLDB: db}, errors.New("driver failed")
	}})
	_, err = factory.make("primary", &config_pb.DBConnection{Driver: proto.String("fake")})
	if err == nil || !strings.Contains(err.Error(), "driver failed") {
		t.Fatalf("error=%v", err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("partial driver DB was not closed")
	}
}

func TestConnectionFactoryDefaultsToMySQL(t *testing.T) {
	called := false
	factory := newConnectionFactory(map[string]DriverFactory{
		"mysql": func(config DriverConfig) (DriverConnection, error) {
			called = true
			db, err := sql.Open(gormsqlite.DriverName, ":memory:")
			if err != nil {
				return DriverConnection{}, err
			}
			return DriverConnection{
				SQLDB:     db,
				Dialector: gormsqlite.New(gormsqlite.Config{Conn: db}),
			}, nil
		},
	})
	t.Cleanup(func() { _ = factory.close() })

	if _, err := factory.make("primary", &config_pb.DBConnection{Dsn: "fixture-dsn"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("default MySQL driver was not called")
	}
}

func TestConnectionFactoryRejectsUnknownDriver(t *testing.T) {
	factory := newConnectionFactory(map[string]DriverFactory{"mysql": stubDriver})
	_, err := factory.make("primary", &config_pb.DBConnection{
		Driver: proto.String("postgres"),
		Dsn:    "fixture-dsn",
	})
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("error = %v", err)
	}
}

func TestConnectionFactoryRejectsIncompleteDriverResult(t *testing.T) {
	db, err := sql.Open(gormsqlite.DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(map[string]DriverFactory{
		"broken": func(DriverConfig) (DriverConnection, error) {
			return DriverConnection{SQLDB: db}, nil
		},
	})
	_, err = factory.make("primary", &config_pb.DBConnection{
		Driver: proto.String("broken"),
		Dsn:    "fixture-dsn",
	})
	if err == nil {
		t.Fatal("incomplete driver result accepted")
	}
	if pingErr := db.Ping(); pingErr == nil {
		t.Fatalf("driver SQL DB was not closed: %v", pingErr)
	}
}

func TestConnectionFactoryClosesPoolsInReverseConstructionOrder(t *testing.T) {
	for _, failedConstruction := range []bool{false, true} {
		t.Run(fmt.Sprintf("construction_failed=%t", failedConstruction), func(t *testing.T) {
			var closed []string
			driverName := fmt.Sprintf("database_cleanup_order_%d", mysqlMetricsDriverID.Add(1))
			sql.Register(driverName, &orderedCloseDriver{closed: &closed})
			factory := newConnectionFactory(nil)
			t.Cleanup(func() {
				if err := factory.close(); err != nil {
					t.Error(err)
				}
			})
			for _, name := range []string{"analytics", "primary", "warehouse"} {
				db, err := sql.Open(driverName, name)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Ping(); err != nil {
					t.Fatal(err)
				}
				if err := factory.track(connectionPool{db: db, name: name}); err != nil {
					t.Fatal(err)
				}
			}
			if failedConstruction {
				cause := errors.New("construction failed")
				if err := closeConnectionFactoryAfterError(factory, cause); !errors.Is(err, cause) {
					t.Fatalf("rollback error=%v", err)
				}
			} else if err := factory.close(); err != nil {
				t.Fatal(err)
			}
			if want := []string{"warehouse", "primary", "analytics"}; !reflect.DeepEqual(closed, want) {
				t.Fatalf("closed=%v want=%v", closed, want)
			}
		})
	}
}

type orderedCloseDriver struct{ closed *[]string }

func (d *orderedCloseDriver) Open(name string) (driver.Conn, error) {
	return &orderedCloseConn{mysqlMetricsScriptConn: &mysqlMetricsScriptConn{}, name: name, closed: d.closed}, nil
}

type orderedCloseConn struct {
	*mysqlMetricsScriptConn
	name   string
	closed *[]string
}

func (c *orderedCloseConn) Close() error { *c.closed = append(*c.closed, c.name); return nil }

func TestPoolExpansionKeepsRequestedIdleSize(t *testing.T) {
	db := newPoolTestDB(t)
	configConnectionPool(db, &config_pb.DBConnection{MaxOpenConns: proto.Int32(1), MaxIdleConns: proto.Int32(1)})
	configConnectionPool(db, &config_pb.DBConnection{MaxOpenConns: proto.Int32(4), MaxIdleConns: proto.Int32(4)})
	fillIdlePool(t, db, 4)
	if got := db.Stats().Idle; got != 4 {
		t.Fatalf("expanded idle connections = %d, want 4", got)
	}
}

func TestPoolRemovedLimitsRestoreDefaultsAndExplicitZeroDisablesIdle(t *testing.T) {
	for _, tt := range []struct {
		name string
		next *config_pb.DBConnection
		idle int
	}{
		{"removed", &config_pb.DBConnection{}, 2},
		{"explicit zero", &config_pb.DBConnection{MaxIdleConns: proto.Int32(0)}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := newPoolTestDB(t)
			configConnectionPool(db, &config_pb.DBConnection{MaxOpenConns: proto.Int32(1), MaxIdleConns: proto.Int32(1)})
			configConnectionPool(db, tt.next)
			if got := db.Stats().MaxOpenConnections; got != 0 {
				t.Fatalf("removed max open = %d, want unlimited", got)
			}
			fillIdlePool(t, db, 3)
			if got := db.Stats().Idle; got != tt.idle {
				t.Fatalf("idle connections = %d, want %d", got, tt.idle)
			}
		})
	}
}

func TestPoolRemovedExpiryDoesNotKeepClosingConnections(t *testing.T) {
	for _, tt := range []struct {
		name    string
		initial *config_pb.DBConnection
	}{
		{"lifetime", &config_pb.DBConnection{ConnMaxLifetime: durationpb.New(time.Second)}},
		{"idle time", &config_pb.DBConnection{ConnMaxIdleTime: durationpb.New(time.Second)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := newPoolTestDB(t)
				configConnectionPool(db, tt.initial)
				fillIdlePool(t, db, 1)
				configConnectionPool(db, &config_pb.DBConnection{})
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if err := db.PingContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				if stats := db.Stats(); stats.MaxLifetimeClosed != 0 || stats.MaxIdleTimeClosed != 0 {
					t.Fatalf("removed expiry still closed connections: %+v", stats)
				}
			})
		})
	}
}

func newPoolTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(gormsqlite.DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func fillIdlePool(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conns := make([]*sql.Conn, 0, count)
	defer func() {
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	for range count {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
}
