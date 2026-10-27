//go:build demo

package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	redislock "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/lock/redis"
	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	queuegorm "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database/gorm"
	queueredis "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/cache"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka/outbox"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/observability"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"
)

const (
	roleAPI       = "api"
	roleInventory = "inventory"
	roleGateway   = "gateway"
	roleCron      = "cron"
	roleWorker    = "worker"
	roleDaemon    = "daemon"
	roleLoadgen   = "loadgen"
	roleReport    = "report"

	taskQueue    = "shop-tasks"
	delayedQueue = "shop-delayed"
	eventTopic   = "order-events"
	deadTopic    = "order-events-dlq"
	// outboxTopic 是外盒消息选择 Producer 的键，对应 Kafka Topic order-events。
	outboxTopic = "orders"
)

var errScenario = errors.New("demo scenario failure")

func currentRole() string { return os.Getenv("DEMO_ROLE") }

func validRole(role string) bool {
	switch role {
	case roleAPI, roleInventory, roleGateway, roleCron, roleWorker, roleDaemon, roleLoadgen, roleReport:
		return true
	}
	return false
}

func needsDatabase(role string) bool {
	return role == roleAPI || role == roleCron || role == roleWorker || role == roleReport
}

type configPath string

type order struct {
	ID        string `gorm:"primaryKey;size:64"`
	SKU       string `gorm:"size:32;index"`
	Status    string `gorm:"size:16;index"`
	CreatedAt time.Time
}

// shop 按角色持有所需组件；未使用的组件保持 nil，由 Wire 构造的 Manager 统一释放连接。
type shop struct {
	role     string
	logger   log.Logger
	db       database.Manager
	redis    *redis.Client
	clients  client.Factory
	cache    *cache.Cache[order]
	outbox   *outbox.Outbox
	relay    *queue.Worker[outbox.Message]
	locker   lock.Locker
	orders   metric.Int64Counter
	producer kafka.Producer
	tasks    *queue.Queue[string]
	delayed  *queue.Queue[string]
	worker   *queue.Worker[string]
	consumer *kafka.ConsumerRuntime
}

func newSpec(application *app.Spec, servers *server.Spec, jobs *job.Spec, path configPath) (*bootstrap.Spec, error) {
	if _, err := os.Stat(string(path)); err != nil {
		return nil, fmt.Errorf("stat configuration: %w", err)
	}
	return bootstrap.NewSpec(application, servers, jobs, bootstrap.ConfigSources{}).Configuration(file.AddConfigSource(string(path))), nil
}

func newShop(db database.Manager, redisManager foundationredis.Manager, kafkaFactory *kafka.ClientFactory, clients client.Factory,
	provider metrics.Provider, tracer tracing.Provider, logger log.Logger) (_ *shop, cleanup func(), err error) {
	var releases []func()
	cleanup = func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	role := currentRole()
	s := &shop{role: role, logger: logger.WithModule("shop"), db: db, redis: redisManager.Default(), clients: clients}
	if s.orders, err = provider.Meter("demo.shop").Int64Counter("business_orders_total"); err != nil {
		return nil, cleanup, err
	}
	cacheMetrics, err := metrics.NewCacheMetrics(provider, "orders")
	if err != nil {
		return nil, cleanup, err
	}
	// 订单旁路缓存：同一订单并发未命中只回源一次，不存在的订单缓存 30 秒，避免反复打到数据库。
	if s.cache, err = cache.New[order](s.redis, cache.Config{KeyPrefix: "shop:order", TTL: 10 * time.Minute, NegativeTTL: 30 * time.Second, Metrics: cacheMetrics}); err != nil {
		return nil, cleanup, err
	}
	if s.locker, err = redislock.NewDefault(redisManager); err != nil {
		return nil, cleanup, err
	}
	if s.locker, err = lock.WithMetrics(s.locker, "orders", provider); err != nil {
		return nil, cleanup, err
	}
	var outboxRepo *queuegorm.SimpleRepo
	if role == roleAPI {
		if outboxRepo, err = queuegorm.NewSimpleRepo(context.Background(), db, queuegorm.SimpleConfig{Table: "order_outbox"}); err != nil {
			return nil, cleanup, err
		}
		// 依赖可能晚于应用就绪，建表重试有界；失败交给 Kubernetes 重启。
		// 配置了只读副本时迁移必须走主库：database.AutoMigrate 固定在主库检查与建表。
		var migrateErr error
		for range 30 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			migrateErr = database.AutoMigrate(ctx, db, &order{})
			if migrateErr == nil {
				migrateErr = outboxRepo.Migrate(ctx)
			}
			cancel()
			if migrateErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if migrateErr != nil {
			return nil, cleanup, fmt.Errorf("migrate orders: %w", migrateErr)
		}
	}
	// 同一份观测依赖可直接传给 Kafka、Queue 与 Outbox。
	obs := observability.New(s.logger, tracer, provider)
	if role == roleAPI || role == roleWorker {
		topic := eventTopic
		if role == roleWorker {
			topic = deadTopic
		}
		producer, release, createErr := kafka.NewProducerWithObservability(kafkaFactory, kafka.ProducerConfig{Connection: "main", Topic: topic}, obs)
		if createErr != nil {
			return nil, cleanup, createErr
		}
		releases = append(releases, release)
		s.producer = producer
	}
	if role == roleAPI {
		// 下单事件经事务外盒发布：与订单同一事务写入，提交后由本 Pod 的 Relay 发布；多 Pod 经 SKIP LOCKED 分摊。
		if s.outbox, err = outbox.New("order-outbox", databasequeue.NewStore(outboxRepo), obs); err != nil {
			return nil, cleanup, err
		}
		if s.relay, err = s.outbox.Relay(outbox.RelayConfig{
			Producers: map[string]kafka.Producer{outboxTopic: s.producer},
			Worker:    queue.WorkerConfig{Name: "order-outbox-relay", PollInterval: 100 * time.Millisecond},
		}); err != nil {
			return nil, cleanup, err
		}
	}
	if role == roleAPI || role == roleWorker || role == roleCron {
		for _, name := range []string{taskQueue, delayedQueue} {
			store, createErr := queueredis.NewStore(redisManager, queueredis.Config{Connection: "main", KeyPrefix: name})
			if createErr != nil {
				return nil, cleanup, createErr
			}
			q, createErr := queue.NewQueue(queue.Definition[string]{Queue: name, Version: 1, Codec: textCodec{}}, store, obs)
			if createErr != nil {
				return nil, cleanup, createErr
			}
			if role == roleWorker {
				// 库存统计只由一个角色采集，避免多个应用重复上报同一逻辑队列。
				unregister, statErr := queue.RegisterStats(name, store, provider, 5*time.Second)
				if statErr != nil {
					return nil, cleanup, statErr
				}
				releases = append(releases, func() { _ = unregister() })
			}
			if name == delayedQueue {
				s.delayed = q
				continue
			}
			s.tasks = q
			if role == roleWorker {
				if s.worker, err = q.WorkerWithExecution(func(ctx context.Context, execution queue.Execution[string]) error {
					return s.handleTask(ctx, execution.ID, execution.Message)
				}, queue.WorkerConfig{
					// RenewLease：偶发的 12 秒长任务也只用 6 秒租约，执行期间每 2 秒续租；崩溃后 6 秒内即可被重新领取。
					Name: "order-notifier", Concurrency: 4, PollInterval: 200 * time.Millisecond,
					RenewLease: true, Lease: 6 * time.Second, StorageTimeout: time.Second,
					Retry: &queue.RetryPolicy{MaxAttempts: 3, MinBackoff: 500 * time.Millisecond, MaxBackoff: 2 * time.Second},
				}); err != nil {
					return nil, cleanup, err
				}
			}
		}
	}
	if role == roleWorker {
		rawConsumer, createErr := kafka.NewConsumer(kafkaFactory, s.logger, kafka.ConsumerConfig{
			// 静态组成员身份使用 Pod 名，滚动发布时新旧实例互不顶替。
			Connection: "main", Topic: eventTopic, Group: "order-worker", Instance: os.Getenv("HOSTNAME"), Concurrency: 2,
		})
		if createErr != nil {
			return nil, cleanup, createErr
		}
		if s.consumer, err = kafka.NewConsumerRuntime(kafka.RuntimeConfig{
			Name: "order-events", Destination: eventTopic,
			Retry:      &kafka.RetryPolicy{MaxAttempts: 3, MinBackoff: 200 * time.Millisecond, MaxBackoff: time.Second},
			DeadLetter: s.producer, DeadLetterDestination: deadTopic,
		}, rawConsumer, s.handleEvent, obs); err != nil {
			return nil, cleanup, err
		}
	}
	return s, cleanup, nil
}

// boot 按角色登记 HTTP 路由、任务、运行时与就绪检查；启动与停止由应用统一负责。
func boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, s *shop) (bootstrap.Bootstrap, error) {
	if needsDatabase(s.role) {
		spec.Health().Checks(server.ReadinessCheck{Name: "mysql", Check: func(ctx context.Context) error {
			return s.db.Connection(ctx).Exec("SELECT 1").Error
		}})
	}
	switch s.role {
	case roleAPI:
		spec.HTTP().Register(
			server.HandleRoute(http.MethodPost, "/orders", "/shop.Orders/Create", s.createOrder),
			server.HandleRoute(http.MethodGet, "/orders/{id}", "/shop.Orders/Get", s.getOrder),
		)
		spec.RegisterRuntime(s.relay)
	case roleInventory:
		spec.HTTP().Register(server.HandleRoute(http.MethodGet, "/stock/{sku}", "/shop.Inventory/Stock", s.stock))
	case roleGateway:
		spec.HTTP().Register(
			server.HandleRoute(http.MethodPost, "/api/orders", "/gateway.Orders/Create", s.proxyCreate),
			server.HandleRoute(http.MethodGet, "/api/orders/{id}", "/gateway.Orders/Get", s.proxyGet),
		)
	case roleCron:
		// 两个副本共享 Redis 锁，每次触发只执行一次；另一副本记 job_triggers_skipped_total{reason="cluster_locked"}。
		spec.Job().RegisterCron("reconcile-orders", "@every 15s", job.TaskFunc(s.reconcile), job.WithClusterLock(s.locker, "shop:job:reconcile-orders", 10*time.Second))
		spec.Job().RegisterCron("cleanup-orders", "@every 30s", job.TaskFunc(s.cleanupOrders), job.WithClusterLock(s.locker, "shop:job:cleanup-orders", 20*time.Second))
		spec.Job().RegisterCron("settlement-report", "@every 1m", job.TaskFunc(s.settlement))
		spec.Job().RegisterCron("schedule-reminders", "@every 20s", job.TaskFunc(s.scheduleReminders))
	case roleWorker:
		spec.RegisterKafkaConsumer(s.consumer).RegisterRuntime(s.worker)
	case roleDaemon:
		spec.RegisterRuntime(&statsDaemon{shop: s, stop: make(chan struct{})})
		spec.Job().RegisterDaemon("heartbeat", job.TaskFunc(s.heartbeat))
		// 每 5 分钟的前 40 秒依赖检查失败，演示就绪摘除与面板上的就绪率变化。
		spec.Health().Checks(server.ReadinessCheck{Name: "upstream-window", Check: func(context.Context) error {
			if time.Now().Minute()%5 == 0 && time.Now().Second() < 40 {
				return errors.New("maintenance window")
			}
			return nil
		}})
	case roleLoadgen:
		spec.RegisterRuntime(&loadGenerator{shop: s, stop: make(chan struct{})})
	case roleReport:
		spec.DisableServiceRegistration()
		spec.Job().RegisterOnce("daily-report", job.TaskFunc(s.dailyReport)).ExitWhenDone()
	}
	return bootstrap.Bootstrap{}, nil
}

func (s *shop) createOrder(ctx context.Context, req *server.HTTPRequest) (any, error) {
	sku := req.Query("sku")
	if sku == "" {
		sku = fmt.Sprintf("sku-%02d", rand.IntN(20))
	}
	lease, err := s.locker.TryLock(ctx, "sku:"+sku, 5*time.Second)
	if errors.Is(err, lock.ErrNotAcquired) {
		s.orders.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "conflict")))
		return nil, foundationerrors.New(http.StatusConflict, "SKU_BUSY", "sku is being ordered")
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Unlock(context.WithoutCancel(ctx)) }()
	inventory, release, err := client.AcquireHTTP(ctx, s.clients, "inventory")
	if err != nil {
		return nil, err
	}
	defer release()
	var stock map[string]int
	if err := inventory.Invoke(ctx, http.MethodGet, "/stock/"+sku, nil, &stock, kratoshttp.Operation("/shop.Inventory/Stock")); err != nil {
		s.orders.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "out_of_stock")))
		return nil, err
	}
	item := order{ID: uuid.NewString(), SKU: sku, Status: "created", CreatedAt: time.Now()}
	if err := s.db.Transaction(ctx, func(ctx context.Context) error {
		if rand.IntN(100) < 3 {
			// 少量慢 SQL 演示慢查询与 P99 尾延迟。
			if err := s.db.Connection(ctx).Exec("SELECT SLEEP(0.25)").Error; err != nil {
				return err
			}
		}
		if err := s.db.Connection(ctx).Create(&item).Error; err != nil {
			return err
		}
		// 订单事件与订单同一事务提交，回滚则不发布；消息 ID 等于订单 ID，消费者据此幂等。
		_, err := s.outbox.Write(ctx, outbox.Message{ID: "order-created:" + item.ID, Topic: outboxTopic, Key: []byte(sku), Body: []byte(pickScenario())})
		return err
	}); err != nil {
		return nil, err
	}
	// 写后预热缓存，随后的查单直接命中，不读只读副本。
	_ = s.cache.Set(ctx, item.ID, item)
	scenario := pickScenario()
	if _, err := s.tasks.PostWith(ctx, scenario, queue.PostOptions{ID: item.ID}); err != nil {
		return nil, err
	}
	s.orders.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "created")))
	return map[string]string{"id": item.ID, "sku": sku}, nil
}

// getOrder 走旁路缓存；未命中时从只读副本回源，不存在的订单缓存 30 秒。
func (s *shop) getOrder(ctx context.Context, req *server.HTTPRequest) (any, error) {
	id := req.PathParam("id")
	item, err := s.cache.Get(ctx, id, func(ctx context.Context) (order, error) {
		var item order
		err := s.db.Connection(ctx).First(&item, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return item, cache.ErrNotFound
		}
		return item, err
	})
	if errors.Is(err, cache.ErrNotFound) {
		return nil, foundationerrors.New(http.StatusNotFound, "ORDER_NOT_FOUND", "order not found")
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

// stock 模拟下游库存服务：常规 5–80ms，4% 慢于 1s（触发延迟 SLO），5% 暂时不可用，0.5% panic（被框架恢复）。
func (s *shop) stock(ctx context.Context, req *server.HTTPRequest) (any, error) {
	if rand.IntN(1000) < 5 {
		panic("demo: inventory handler bug")
	}
	delay := time.Duration(5+rand.IntN(75)) * time.Millisecond
	if rand.IntN(100) < 4 {
		delay = 1200 * time.Millisecond
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(delay):
	}
	if rand.IntN(100) < 5 {
		return nil, foundationerrors.New(http.StatusServiceUnavailable, "STOCK_UNAVAILABLE", "inventory temporarily unavailable")
	}
	return map[string]int{req.PathParam("sku"): 10 + rand.IntN(90)}, nil
}

func (s *shop) proxyCreate(ctx context.Context, req *server.HTTPRequest) (any, error) {
	return s.proxy(ctx, http.MethodPost, "/orders?sku="+req.Query("sku"), "/shop.Orders/Create")
}

func (s *shop) proxyGet(ctx context.Context, req *server.HTTPRequest) (any, error) {
	return s.proxy(ctx, http.MethodGet, "/orders/"+req.PathParam("id"), "/shop.Orders/Get")
}

func (s *shop) proxy(ctx context.Context, method, path, operation string) (any, error) {
	upstream, release, err := client.AcquireHTTP(ctx, s.clients, "orders")
	if err != nil {
		return nil, err
	}
	defer release()
	var reply map[string]any
	if err := upstream.Invoke(ctx, method, path, nil, &reply, kratoshttp.Operation(operation)); err != nil {
		return nil, err
	}
	return reply, nil
}

func pickScenario() string {
	switch n := rand.IntN(100); {
	case n < 2:
		// 长任务演示 Worker 执行期间续租。
		return "long"
	case n < 85:
		return "success"
	case n < 95:
		return "retry"
	default:
		return "permanent"
	}
}

func (s *shop) handleTask(ctx context.Context, id, scenario string) error {
	delay := time.Duration(10+rand.IntN(90)) * time.Millisecond
	if scenario == "long" {
		delay = 12 * time.Second
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
	}
	if scenario == "permanent" {
		return queue.Permanent(errScenario)
	}
	return s.firstAttemptFails(ctx, "queue:"+id, scenario)
}

func (s *shop) handleEvent(ctx context.Context, message *kafka.Message) error {
	time.Sleep(time.Duration(5+rand.IntN(40)) * time.Millisecond)
	if string(message.Body) == "permanent" {
		return kafka.Permanent(errScenario)
	}
	return s.firstAttemptFails(ctx, "kafka:"+message.ID, string(message.Body))
}

// firstAttemptFails 让 retry 场景首次失败、重试成功；计数保存在 Redis，跨实例一致。
func (s *shop) firstAttemptFails(ctx context.Context, key, scenario string) error {
	if scenario != "retry" {
		return nil
	}
	attempt, err := s.redis.Eval(ctx, `local n = redis.call('INCR', KEYS[1]); redis.call('EXPIRE', KEYS[1], 3600); return n`, []string{"demo:attempt:" + key}).Int64()
	if err != nil {
		return err
	}
	if attempt == 1 {
		return errScenario
	}
	return nil
}

func (s *shop) reconcile(ctx context.Context) error {
	return s.db.Connection(ctx).Model(&order{}).Where("status = ? AND created_at < ?", "created", time.Now().Add(-time.Minute)).Update("status", "settled").Error
}

// cleanupOrders 删除 10 分钟前的订单；20% 概率失败，演示 Job 失败率与告警。
func (s *shop) cleanupOrders(ctx context.Context) error {
	if rand.IntN(100) < 20 {
		return errors.New("cleanup lock contention")
	}
	return s.db.Connection(ctx).Where("created_at < ?", time.Now().Add(-10*time.Minute)).Delete(&order{}).Error
}

// settlement 模拟耗时 3–8 秒的结算任务，演示 Job 耗时分布。
func (s *shop) settlement(ctx context.Context) error {
	var count int64
	if err := s.db.Connection(ctx).Model(&order{}).Where("status = ?", "settled").Count(&count).Error; err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(3+rand.IntN(5)) * time.Second):
	}
	return nil
}

// scheduleReminders 向无 Worker 的延迟队列投递立即与 1 小时后到期的任务，演示积压与最老就绪年龄。
func (s *shop) scheduleReminders(ctx context.Context) error {
	now := time.Now().UTC()
	for _, at := range []time.Time{now, now.Add(time.Hour)} {
		if _, err := s.delayed.PostWith(ctx, "remind", queue.PostOptions{ID: uuid.NewString(), AvailableAt: at}); err != nil {
			return err
		}
	}
	return nil
}

func (s *shop) heartbeat(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = s.redis.Set(ctx, "demo:heartbeat", time.Now().Unix(), time.Minute).Err()
		}
	}
}

// dailyReport 一次性报表：汇总后退出；30% 概率失败，演示 K8s Job 失败告警。
func (s *shop) dailyReport(ctx context.Context) error {
	var count int64
	if err := s.db.Connection(ctx).Model(&order{}).Count(&count).Error; err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	if rand.IntN(100) < 30 {
		return errors.New("report upload failed")
	}
	s.logger.WithContext(ctx).With("event", "shop.report.completed", "orders", count).Info("report completed")
	return nil
}

type textCodec struct{}

func (textCodec) Encode(value string) ([]byte, error) { return []byte(value), nil }
func (textCodec) Decode(value []byte) (string, error) { return string(value), nil }
