package database_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"slices"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/sqlite"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

type publicManagerRecord struct {
	ID   int `gorm:"primaryKey"`
	Name string
}

func (publicManagerRecord) TableName() string { return "public_manager_records" }

func TestNewManagerBuildsRegisteredSQLiteDriverAndCleansUp(t *testing.T) {
	shared, releaseLogger, err := testlog.New(testlog.Config{
		Level:      kratoslog.LevelInfo,
		TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		},
		File: testlog.FileConfig{OutputConfig: testlog.OutputConfig{
			Disable: true,
			Level:   kratoslog.LevelInfo,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseLogger)

	application := appinfo.New("database-public-test")
	disabled := true
	tracingProvider, releaseTracing, err := tracing.NewProvider(
		testconfig.New(t, "tracing", &config_pb.Tracing{Disable: &disabled}),
		application,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseTracing)
	metricsProvider, releaseMetrics, err := metrics.NewProvider(application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseMetrics)

	configManager := testconfig.New(t, "database", &config_pb.Database{
		Default: proto.String("default"),
		Connections: map[string]*config_pb.DBConnection{
			"default": {
				Driver: proto.String("sqlite3"),
				Dsn:    "file:public-manager?mode=memory&cache=shared",
			},
		},
		Tracing: &config_pb.GormTracing{Disable: &disabled},
		Metrics: &config_pb.GormMetrics{Disable: &disabled},
	})
	manager, cleanup, err := database.NewManager(
		shared,
		configManager,
		application,
		tracingProvider,
		metricsProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || cleanup == nil {
		t.Fatalf(
			"NewManager(): manager nil = %t, cleanup nil = %t",
			manager == nil,
			cleanup == nil,
		)
	}
	t.Cleanup(cleanup)
	if drivers := database.RegisteredDrivers(); !slices.Contains(drivers, "sqlite3") {
		t.Fatalf("RegisteredDrivers() = %v, want sqlite3", drivers)
	}

	ctx := context.Background()
	db := manager.Connection(ctx)
	if err := db.AutoMigrate(&publicManagerRecord{}); err != nil {
		t.Fatal(err)
	}
	want := publicManagerRecord{ID: 7, Name: "sqlite"}
	if err := db.Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	var got publicManagerRecord
	if err := manager.Connection(ctx).First(&got, want.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("loaded record = %#v, want %#v", got, want)
	}
	if err := manager.Connection(database.UseConnection(ctx, "missing")).Error; !errors.Is(err, database.ErrConnectionUnknown) {
		t.Fatalf("UseConnection(missing) error = %v, want ErrConnectionUnknown", err)
	}
	transactionRan := false
	if err := manager.Transaction(ctx, func(context.Context) error {
		transactionRan = true
		return nil
	}, database.WithSQLTxOptions(&sql.TxOptions{})); err != nil || !transactionRan {
		t.Fatalf("Transaction(WithSQLTxOptions) ran=%t err=%v", transactionRan, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("SQLite pool before cleanup: %v", err)
	}

	cleanup()
	cleanup()
	if err := manager.Connection(ctx).Error; !errors.Is(err, database.ErrManagerClosed) {
		t.Fatalf("Connection() after cleanup error = %v", err)
	}
	if err := sqlDB.Ping(); err == nil {
		t.Fatal("cleanup left the registered SQLite pool open")
	}
}
