package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func newSQLiteManager(t *testing.T, conf *config_pb.Database) *manager {
	t.Helper()
	conf.Metrics = &config_pb.GormMetrics{Disable: proto.Bool(true)}
	driver := new(recordingSQLiteDriver)
	m, close, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), newManagerMetricsProvider(), map[string]DriverFactory{"sqlite3": driver.open})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(close)
	return m
}
func TestManagerKeepsNamedDialectsIndependent(t *testing.T) {
	conf := &config_pb.Database{Default: proto.String("primary"), Gorm: &config_pb.Gorm{SkipDefaultTransaction: proto.Bool(true)}, Metrics: &config_pb.GormMetrics{Disable: proto.Bool(true)}, Connections: map[string]*config_pb.DBConnection{
		"primary": {Driver: proto.String("sqlite3"), Dsn: "file:review-dialect-primary?mode=memory&cache=shared"},
		"mysql":   {Driver: proto.String("mysql"), Dsn: "unused-local-dialect-test"},
	}}
	driver := new(recordingSQLiteDriver)
	mysqlFactory := func(config DriverConfig) (DriverConnection, error) {
		// SQLite pool substitutes only the network transport; GORM uses the real MySQL dialect.
		c, e := driver.open(DriverConfig{Name: config.Name, DSN: "file:review-dialect-mysql?mode=memory&cache=shared"})
		if e != nil {
			return c, e
		}
		c.Dialector = gormmysql.New(gormmysql.Config{Conn: c.SQLDB, SkipInitializeWithVersion: true})
		return c, nil
	}
	m, cleanup, err := newManagerWithDrivers(newManagerTestLogger(t), testconfig.New(t, "database", conf), managerTestAppInfo{}, newManagerTracingProvider(true), newManagerMetricsProvider(), map[string]DriverFactory{"sqlite3": driver.open, "mysql": mysqlFactory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	q := m.Connection(UseConnection(context.Background(), "mysql")).Session(&gorm.Session{DryRun: true}).Clauses(clause.OnConflict{UpdateAll: true}).Create(&managerRecord{ID: 1, Name: "value"})
	if q.Error != nil {
		t.Fatal(q.Error)
	}
	t.Logf("target mysql, dialect=%s, SQL=%s", q.Dialector.Name(), q.Statement.SQL.String())
	if q.Dialector.Name() != "mysql" || !strings.Contains(q.Statement.SQL.String(), "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("MySQL connection built the wrong dialect: name=%s SQL=%s", q.Dialector.Name(), q.Statement.SQL.String())
	}
	if strings.Contains(q.Statement.SQL.String(), "RETURNING") {
		t.Error("MySQL upsert contains SQLite RETURNING clause")
	}
	p := m.Connection(UseConnection(context.Background(), "primary"))
	if err := p.AutoMigrate(&managerRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := p.Clauses(clause.OnConflict{UpdateAll: true}).Create(&managerRecord{ID: 1, Name: "value"}).Error; err != nil {
		t.Errorf("SQLite upsert broken by MySQL clause builder: %v", err)
	}
}

func TestNamedConnectionsKeepConfigurationAndAESBoundToTheirTransaction(t *testing.T) {
	const firstKey = "MDEyMzQ1Njc4OWFiY2RlZg=="
	const secondKey = "ZmVkY2JhOTg3NjU0MzIxMA=="
	config := &config_pb.Database{
		Default: proto.String("primary"),
		Gorm:    &config_pb.Gorm{AllowGlobalUpdate: proto.Bool(true), SkipDefaultTransaction: proto.Bool(true)},
		Connections: map[string]*config_pb.DBConnection{
			"primary": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "primary.db"), Aes: &config_pb.DatabaseAes{Key: proto.String(firstKey)}},
			"archive": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "archive.db"), Aes: &config_pb.DatabaseAes{Key: proto.String(secondKey)}, Gorm: &config_pb.Gorm{AllowGlobalUpdate: proto.Bool(false), SkipDefaultTransaction: proto.Bool(false)}},
		},
	}
	manager := newSQLiteManager(t, config)
	ctx := context.Background()
	for _, name := range []string{"primary", "archive"} {
		db := manager.Connection(UseConnection(ctx, name))
		if err := db.AutoMigrate(&aesMapValuesRecord{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&aesMapValuesRecord{Secret: AESDecryptString(name), Plain: name}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Connection(ctx).Model(&aesMapValuesRecord{}).Update("Plain", "allowed").Error; err != nil {
		t.Fatalf("global configuration not inherited: %v", err)
	}
	if err := manager.Connection(UseConnection(ctx, "archive")).Model(&aesMapValuesRecord{}).Update("Plain", "blocked").Error; !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("explicit false override ignored: %v", err)
	}
	err := manager.Transaction(UseConnection(ctx, "archive"), func(txCtx context.Context) error {
		switched := UseConnection(txCtx, "primary")
		if err := manager.Connection(UseConnection(txCtx, "missing")).Error; !errors.Is(err, ErrConnectionUnknown) {
			t.Fatalf("unknown transaction selector=%v", err)
		}
		db := manager.Connection(switched)
		if db.SkipDefaultTransaction || db.AllowGlobalUpdate {
			t.Fatal("transaction inherited another connection's GORM configuration")
		}
		return db.Model(&aesMapValuesRecord{}).Where("plain = ?", "archive").Updates(map[string]any{"Secret": "transaction secret"}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, key, secret string }{{"primary", firstKey, "primary"}, {"archive", secondKey, "transaction secret"}} {
		pool, _ := manager.connectionFactory.pool(tt.name)
		var encrypted string
		if err := pool.db.QueryRow("SELECT secret FROM aes_map_values_records").Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		cipher, err := newAESFieldCipher(&config_pb.DatabaseAes{Key: proto.String(tt.key)})
		if err != nil {
			t.Fatal(err)
		}
		plain, err := cipher.algorithm.DecryptString(encrypted, string(cipher.key))
		if err != nil || plain != tt.secret {
			t.Fatalf("connection=%s decrypted=%q err=%v", tt.name, plain, err)
		}
		var record aesMapValuesRecord
		if err := manager.Connection(UseConnection(ctx, tt.name)).First(&record).Error; err != nil || string(record.Secret) != tt.secret {
			t.Fatalf("connection=%s record=%#v err=%v", tt.name, record, err)
		}
	}
}
