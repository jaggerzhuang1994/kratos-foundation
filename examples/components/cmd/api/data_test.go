package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDataService(t *testing.T) {
	t.Setenv("LOG_FILE_DISABLE", "true")
	logger, closeLog, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLog)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "orders.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	provider, cleanup, err := metrics.NewProvider(appinfo.New("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	client := redis.NewClient(&redis.Options{Addr: "unused"})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	cache := &orderCacheHook{values: map[string]string{}}
	client.AddHook(cache)
	service, err := newDataService(&orderDatabase{db: db}, orderRedis{client}, provider, logger)
	if err != nil {
		t.Fatal(err)
	}
	leases := &orderLocker{}
	service.locker, err = lock.WithMetrics(leases, "orders", provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := service.Run(ctx, "test-order"); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&demoOrder{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(cache.values) != 0 || leases.held {
		t.Fatalf("resources leaked: rows=%d cache=%v held=%v", count, cache.values, leases.held)
	}
	families, err := provider.PrometheusGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]float64{}
	for _, family := range families {
		for _, m := range family.Metric {
			labels := map[string]string{}
			for _, pair := range m.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if m.Counter != nil {
				totals[family.GetName()+":"+labels["operation"]+":"+labels["result"]] += m.Counter.GetValue()
			}
		}
	}
	for key, want := range map[string]float64{
		"business_cache_lookups_total::hit": 3, "business_cache_lookups_total::miss": 1,
		"business_cache_loads_total::success": 1,
		"lock_operations_total:lock:success":  1, "lock_operations_total:try_lock:contended": 1,
		"lock_operations_total:ttl:success": 1, "lock_operations_total:refresh:success": 1,
		"lock_operations_total:unlock:success": 1,
	} {
		if totals[key] != want {
			t.Errorf("%s = %v, want %v", key, totals[key], want)
		}
	}
	t.Run("cache transport failure", func(t *testing.T) {
		cache.failure = errors.New("cache unavailable")
		if err := service.Run(ctx, "failed-order"); !errors.Is(err, cache.failure) {
			t.Fatalf("error = %v", err)
		}
		cache.failure = nil
		var remaining int64
		if err := db.Model(&demoOrder{}).Count(&remaining).Error; err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("failed run leaked %d orders", remaining)
		}
	})
	t.Run("invalid cached payload", func(t *testing.T) {
		cache.values["invalid"] = "{"
		if _, err := service.cachedOrder(ctx, "invalid", "test-order"); err == nil {
			t.Fatal("expected decode error")
		}
	})
	t.Run("source missing", func(t *testing.T) {
		if _, err := service.cachedOrder(ctx, "missing", "missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("failure releases lease", func(t *testing.T) {
		leases.refreshError = errors.New("refresh failed")
		if err := service.exerciseLock(ctx, "failure"); !errors.Is(err, leases.refreshError) {
			t.Fatalf("error = %v", err)
		}
		if leases.held {
			t.Fatal("lease leaked")
		}
	})
}

type orderDatabase struct{ db *gorm.DB }
type orderTransactionKey struct{}

func (d *orderDatabase) Connection(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(orderTransactionKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return d.db.WithContext(ctx)
}
func (d *orderDatabase) Transaction(ctx context.Context, fn func(context.Context) error, _ ...database.TransactionOption) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(context.WithValue(ctx, orderTransactionKey{}, tx)) })
}

type orderRedis struct{ client *redis.Client }

func (r orderRedis) Default() *redis.Client                   { return r.client }
func (r orderRedis) Connection(string) (*redis.Client, error) { return r.client, nil }

type orderCacheHook struct {
	values  map[string]string
	failure error
}

func (h *orderCacheHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *orderCacheHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *orderCacheHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if h.failure != nil {
			cmd.SetErr(h.failure)
			return h.failure
		}
		key := fmt.Sprint(cmd.Args()[1])
		switch cmd.Name() {
		case "get":
			value, found := h.values[key]
			if !found {
				cmd.SetErr(redis.Nil)
				return redis.Nil
			}
			cmd.(*redis.StringCmd).SetVal(value)
		case "set":
			value := cmd.Args()[2]
			if raw, ok := value.([]byte); ok {
				h.values[key] = string(raw)
			} else {
				h.values[key] = fmt.Sprint(value)
			}
			cmd.(*redis.StatusCmd).SetVal("OK")
		case "del":
			delete(h.values, key)
			cmd.(*redis.IntCmd).SetVal(1)
		default:
			return fmt.Errorf("unexpected Redis command %s", cmd.Name())
		}
		return nil
	}
}

type orderLocker struct {
	held         bool
	refreshError error
}

func (l *orderLocker) Lock(_ context.Context, key string, _ time.Duration) (lock.Lease, error) {
	l.held = true
	return &orderLease{owner: l, key: key}, nil
}
func (l *orderLocker) TryLock(context.Context, string, time.Duration) (lock.Lease, error) {
	return nil, lock.ErrNotAcquired
}

type orderLease struct {
	owner *orderLocker
	key   string
}

func (l *orderLease) Key() string                                  { return l.key }
func (l *orderLease) TTL(context.Context) (time.Duration, error)   { return time.Minute, nil }
func (l *orderLease) Refresh(context.Context, time.Duration) error { return l.owner.refreshError }
func (l *orderLease) Unlock(context.Context) error                 { l.owner.held = false; return nil }
