//go:build demo

package main

import (
	"context"
	"encoding/json"
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
	queueredis "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/lock"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	foundationredis "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/redis"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/tracing"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
	cache    *metrics.CacheMetrics
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
	if s.cache, err = metrics.NewCacheMetrics(provider, "orders"); err != nil {
		return nil, cleanup, err
	}
	if s.locker, err = redislock.NewDefault(redisManager); err != nil {
		return nil, cleanup, err
	}
	if s.locker, err = lock.WithMetrics(s.locker, "orders", provider); err != nil {
		return nil, cleanup, err
	}
	if role == roleAPI {
		// 依赖可能晚于应用就绪，建表重试有界；失败交给 Kubernetes 重启。
		var migrateErr error
		for range 30 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			migrateErr = db.Connection(ctx).AutoMigrate(&order{})
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
	obs := kafka.Observability{Logger: s.logger, Metrics: provider, Tracing: tracer}
	queueObs := queue.Observability{Logger: s.logger, Metrics: provider, Tracing: tracer}
	if role == roleAPI || role == roleWorker {
		topic := eventTopic
		if role == roleWorker {
			topic = deadTopic
		}
		raw, release, createErr := kafka.NewProducer(kafkaFactory, kafka.ProducerConfig{Connection: "main", Topic: topic})
		if createErr != nil {
			return nil, cleanup, createErr
		}
		releases = append(releases, release)
		if s.producer, err = kafka.NewManagedProducer(topic, raw, obs); err != nil {
			return nil, cleanup, err
		}
	}
	if role == roleAPI || role == roleWorker || role == roleCron {
		for _, name := range []string{taskQueue, delayedQueue} {
			store, createErr := queueredis.NewStore(redisManager, queueredis.Config{Connection: "main", KeyPrefix: name})
			if createErr != nil {
				return nil, cleanup, createErr
			}
			q, createErr := queue.NewQueue(queue.Definition[string]{Queue: name, Version: 1, Codec: textCodec{}}, store, queueObs)
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
					Name: "order-notifier", Concurrency: 4, PollInterval: 200 * time.Millisecond,
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
		spec.Http().Register(func(srv server.HTTPServer) error {
			route(srv, http.MethodPost, "/orders", "/shop.Orders/Create", s.createOrder)
			route(srv, http.MethodGet, "/orders/{id}", "/shop.Orders/Get", s.getOrder)
			return nil
		})
	case roleInventory:
		spec.Http().Register(func(srv server.HTTPServer) error {
			route(srv, http.MethodGet, "/stock/{sku}", "/shop.Inventory/Stock", s.stock)
			return nil
		})
	case roleGateway:
		spec.Http().Register(func(srv server.HTTPServer) error {
			route(srv, http.MethodPost, "/api/orders", "/gateway.Orders/Create", s.proxyCreate)
			route(srv, http.MethodGet, "/api/orders/{id}", "/gateway.Orders/Get", s.proxyGet)
			return nil
		})
	case roleCron:
		spec.Job().RegisterCron("reconcile-orders", "@every 15s", job.TaskFunc(s.reconcile))
		spec.Job().RegisterCron("cleanup-orders", "@every 30s", job.TaskFunc(s.cleanupOrders))
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

// route 注册手写路由：固定 operation，并经过默认中间件链产生访问日志与请求指标。
func route(srv server.HTTPServer, method, path, operation string, handler func(context.Context, map[string]string) (any, error)) {
	srv.Route("/").Handle(method, path, func(ctx kratoshttp.Context) error {
		kratoshttp.SetOperation(ctx, operation)
		params := map[string]string{}
		for key, values := range ctx.Vars() {
			params[key] = values[0]
		}
		for key, values := range ctx.Request().URL.Query() {
			params[key] = values[0]
		}
		result, err := ctx.Middleware(func(ctx context.Context, _ any) (any, error) { return handler(ctx, params) })(ctx, nil)
		if err != nil {
			return err
		}
		return ctx.Result(http.StatusOK, result)
	})
}

func (s *shop) createOrder(ctx context.Context, params map[string]string) (any, error) {
	sku := params["sku"]
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
	inventory, _, release, err := s.clients.AcquireClient(ctx, "inventory")
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
		return s.db.Connection(ctx).Create(&item).Error
	}); err != nil {
		return nil, err
	}
	if value, err := json.Marshal(item); err == nil {
		_ = s.redis.Set(ctx, "order:"+item.ID, value, 10*time.Minute).Err()
	}
	scenario := pickScenario()
	if _, err := s.tasks.PostWith(ctx, scenario, queue.PostOptions{ID: item.ID}); err != nil {
		return nil, err
	}
	if err := s.producer.Publish(ctx, &kafka.Message{ID: item.ID, Key: []byte(sku), Body: []byte(pickScenario())}); err != nil {
		return nil, err
	}
	s.orders.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "created")))
	return map[string]string{"id": item.ID, "sku": sku}, nil
}

func (s *shop) getOrder(ctx context.Context, params map[string]string) (any, error) {
	id := params["id"]
	value, err := s.redis.Get(ctx, "order:"+id).Bytes()
	switch {
	case err == nil:
		s.cache.Hit(ctx)
		var item order
		if err := json.Unmarshal(value, &item); err != nil {
			return nil, err
		}
		return item, nil
	case !errors.Is(err, redis.Nil):
		s.cache.Error(ctx)
		return nil, err
	}
	s.cache.Miss(ctx)
	started := time.Now()
	var item order
	err = s.db.Connection(ctx).First(&item, "id = ?", id).Error
	s.cache.Load(ctx, time.Since(started), err)
	if err != nil {
		return nil, foundationerrors.New(http.StatusNotFound, "ORDER_NOT_FOUND", "order not found")
	}
	return item, nil
}

// stock 模拟下游库存服务：常规 5–80ms，2% 慢请求，5% 暂时不可用。
func (s *shop) stock(ctx context.Context, params map[string]string) (any, error) {
	delay := time.Duration(5+rand.IntN(75)) * time.Millisecond
	if rand.IntN(100) < 2 {
		delay = 400 * time.Millisecond
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(delay):
	}
	if rand.IntN(100) < 5 {
		return nil, foundationerrors.New(http.StatusServiceUnavailable, "STOCK_UNAVAILABLE", "inventory temporarily unavailable")
	}
	return map[string]int{params["sku"]: 10 + rand.IntN(90)}, nil
}

func (s *shop) proxyCreate(ctx context.Context, params map[string]string) (any, error) {
	return s.proxy(ctx, http.MethodPost, "/orders?sku="+params["sku"], "/shop.Orders/Create")
}

func (s *shop) proxyGet(ctx context.Context, params map[string]string) (any, error) {
	return s.proxy(ctx, http.MethodGet, "/orders/"+params["id"], "/shop.Orders/Get")
}

func (s *shop) proxy(ctx context.Context, method, path, operation string) (any, error) {
	upstream, _, release, err := s.clients.AcquireClient(ctx, "orders")
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
	case n < 85:
		return "success"
	case n < 95:
		return "retry"
	default:
		return "permanent"
	}
}

func (s *shop) handleTask(ctx context.Context, id, scenario string) error {
	time.Sleep(time.Duration(10+rand.IntN(90)) * time.Millisecond)
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
