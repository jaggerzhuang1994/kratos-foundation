package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redislock "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/lock/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type demoOrder struct {
	ID     string `gorm:"primaryKey"`
	Status string
}

type dataService struct {
	logger log.Logger
	db     database.Manager
	cache  *redis.Client
	counts *metrics.CacheMetrics
	locker lock.Locker
}

func newDataService(db database.Manager, cache foundationredis.Manager, provider metrics.Provider, logger log.Logger) (*dataService, error) {
	counts, err := metrics.NewCacheMetrics(provider, "orders")
	if err != nil {
		return nil, err
	}
	locker, err := redislock.NewDefault(cache)
	if err != nil {
		return nil, err
	}
	locker, err = lock.WithMetrics(locker, "orders", provider)
	if err != nil {
		return nil, err
	}
	// 示例启动时建表；正式服务应由部署流程管理迁移。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.Connection(ctx).AutoMigrate(&demoOrder{}); err != nil {
		return nil, fmt.Errorf("migrate demo orders: %w", err)
	}
	return &dataService{logger: logger.WithModule("data"), db: db, cache: cache.Default(), counts: counts, locker: locker}, nil
}

func (s *dataService) Run(ctx context.Context, runID string) (runErr error) {
	key := "components:orders:" + runID
	order := demoOrder{ID: runID, Status: "created"}
	if err := s.db.Connection(ctx).Create(&order).Error; err != nil {
		return fmt.Errorf("create demo order: %w", err)
	}
	deleted := false
	defer func() {
		// 请求失败或取消后仍给清理独立上限，避免把演示记录遗留到下一轮。
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if !deleted {
			runErr = errors.Join(runErr, s.db.Connection(cleanupCtx).Delete(&order).Error)
		}
		runErr = errors.Join(runErr, s.cache.Del(cleanupCtx, key).Err())
	}()
	if err := s.db.Transaction(ctx, func(txCtx context.Context) error {
		var stored demoOrder
		if err := s.db.Connection(txCtx).First(&stored, "id = ?", runID).Error; err != nil {
			return err
		}
		return s.db.Connection(txCtx).Model(&stored).Update("status", "paid").Error
	}); err != nil {
		return fmt.Errorf("pay demo order transaction: %w", err)
	}
	// 用真实递归汇总模拟较慢的报表 SQL；只更新本轮记录，不通过 sleep 或手动指标造样本。
	// Exec 等待执行结束，避免 Rows/Scan 的迭代耗时位于 GORM row 回调之外。
	if err := s.db.Connection(ctx).Exec(`WITH RECURSIVE batch(n) AS (
		VALUES(1) UNION ALL SELECT n+1 FROM batch WHERE n < 1000000
	) UPDATE demo_orders SET status = CASE WHEN (SELECT sum(n) FROM batch) = 500000500000
		THEN 'paid' ELSE 'invalid' END WHERE id = ?`, runID).Error; err != nil {
		return fmt.Errorf("summarize demo orders: %w", err)
	}
	// 唯一业务 ID 隔离每轮缓存：首次读取回源，其后三次读取验证实际命中。
	for range 4 {
		stored, err := s.cachedOrder(ctx, key, runID)
		if err != nil {
			return err
		}
		if stored.Status != "paid" {
			return fmt.Errorf("unexpected demo order status %q", stored.Status)
		}
	}
	if err := s.exerciseLock(ctx, key); err != nil {
		return err
	}
	if err := s.db.Connection(ctx).Delete(&order).Error; err != nil {
		return fmt.Errorf("delete demo order: %w", err)
	}
	deleted = true
	var missing demoOrder
	if err := s.db.Connection(ctx).First(&missing, "id = ?", runID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		if err != nil {
			return fmt.Errorf("verify deleted demo order: %w", err)
		}
		return errors.New("deleted demo order still exists")
	}
	s.logger.WithContext(ctx).With("run_id", runID, "cache_hits", 3, "cache_misses", 1).Info("Completed the database and cache demonstration")
	return nil
}

func (s *dataService) cachedOrder(ctx context.Context, key, id string) (demoOrder, error) {
	var order demoOrder
	value, err := s.cache.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		if err := json.Unmarshal(value, &order); err != nil {
			s.counts.Error(ctx)
			return order, fmt.Errorf("decode cached order: %w", err)
		}
		s.counts.Hit(ctx)
		return order, nil
	case !errors.Is(err, redis.Nil):
		s.counts.Error(ctx)
		return order, fmt.Errorf("read cached order: %w", err)
	}
	s.counts.Miss(ctx)
	started := time.Now()
	err = s.db.Connection(ctx).First(&order, "id = ?", id).Error
	// Load 只描述 SQL 回源，缓存写入失败不能改写已经成功的回源结果。
	s.counts.Load(ctx, time.Since(started), err)
	if err != nil {
		return order, fmt.Errorf("load order from database: %w", err)
	}
	value, err = json.Marshal(order)
	if err != nil {
		return order, fmt.Errorf("encode cached order: %w", err)
	}
	if err := s.cache.Set(ctx, key, value, time.Minute).Err(); err != nil {
		s.counts.Error(ctx)
		return order, fmt.Errorf("write cached order: %w", err)
	}
	return order, nil
}

func (s *dataService) exerciseLock(ctx context.Context, key string) (runErr error) {
	lease, err := s.locker.Lock(ctx, key, time.Minute)
	if err != nil {
		return fmt.Errorf("acquire order lease: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, lease.Unlock(cleanupCtx))
	}()
	// 用第二个获取请求验证已持有租约的排他性，无需启动额外 goroutine。
	contender, err := s.locker.TryLock(ctx, key, time.Minute)
	if !errors.Is(err, lock.ErrNotAcquired) {
		if err != nil {
			return fmt.Errorf("try competing order lease: %w", err)
		}
		return errors.Join(errors.New("competing order lease unexpectedly acquired"), contender.Unlock(ctx))
	}
	if ttl, err := lease.TTL(ctx); err != nil {
		return fmt.Errorf("read order lease TTL: %w", err)
	} else if ttl <= 0 {
		return errors.New("order lease has no remaining TTL")
	}
	if err := lease.Refresh(ctx, time.Minute); err != nil {
		return fmt.Errorf("refresh order lease: %w", err)
	}
	s.logger.WithContext(ctx).With("key", key, "contended", true).Info("Refreshed the distributed lock lease")
	return nil
}
