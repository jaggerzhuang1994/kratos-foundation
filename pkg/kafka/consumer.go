package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"

	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"
)

// recordCommitter 是成功处理完整批次后提交 Kafka 位点的外部边界。
type recordCommitter interface {
	CommitRecords(context.Context, ...*kgo.Record) error
}

// consumerClient 收拢单个 Kafka 消费实例使用的网络操作，便于在不连接 Broker 的情况下
// 验证 Consume 的并发创建、取消和关闭边界。
type consumerClient interface {
	recordCommitter
	PollRecords(context.Context, int) kgo.Fetches
	AllowRebalance()
	LeaveGroupContext(context.Context) error
	CloseAllowingRebalance()
}

const leaveGroupTimeout = 5 * time.Second

type consumerClientFactory func(context.Context, string, string, ...kgo.Opt) (consumerClient, error)

type consumer struct {
	// createClient 为各消费并发实例创建独立客户端。
	createClient consumerClientFactory
	// logger 消费过程日志。
	logger log.Logger
	// config 经过默认值展开和校验的消费配置。
	config ConsumerConfig
	// running 原子标记当前 Consume 是否运行，阻止并发启动。
	running atomic.Bool
}

var _ Consumer = (*consumer)(nil)

// newConsumer 只替换 Kafka 网络边界，生产构造仍由 NewConsumer 负责校验 ClientFactory 与连接。
func newConsumer(
	config ConsumerConfig,
	logger log.Logger,
	createClient consumerClientFactory,
) Consumer {
	if logger != nil {
		logger = logger.WithModule("kafka")
	}
	return &consumer{createClient: createClient, logger: logger, config: config}
}

// Consume 按 Concurrency 创建独立 Client，暂时性 Kafka 操作失败只恢复对应实例。
func (c *consumer) Consume(ctx context.Context, handler DeliveryHandler) error {
	if handler == nil {
		return errors.New("kafka queue delivery handler is nil")
	}
	if c == nil || c.createClient == nil {
		return errors.New("kafka queue consumer is not initialized")
	}
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("kafka queue consumer is already running")
	}
	defer c.running.Store(false)
	group, groupCtx := errgroup.WithContext(ctx)
	for index := 0; index < c.config.Concurrency; index++ {
		instance := consumerInstance(c.config, index)
		group.Go(func() error {
			return c.consume(groupCtx, instance, handler)
		})
	}
	return group.Wait()
}

// consume 在单个并发槽内恢复暂时失败；重建后从已提交位点重新消费。
func (c *consumer) consume(ctx context.Context, instance string, handler DeliveryHandler) error {
	var backoff reconnect.Backoff
	attempt := 0
	firstReadRetries := 0
	var recoveryOptions []kgo.Opt
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		progressed, err := c.consumeClient(ctx, instance, handler, recoveryOptions...)
		if progressed {
			attempt = 0
			firstReadRetries = 0
		}
		// 首读 EOF 既可能是 Broker 重启，也可能是协议配置错误，最多额外重建三次。
		// 只有实际提交成功才重置预算；不放宽明确的认证、授权或 TLS 证书错误。
		if !recoverableConsumerOperation(err, firstReadRetries < 3) {
			return err
		}
		var firstRead *kgo.ErrFirstReadEOF
		if errors.As(err, &firstRead) {
			firstReadRetries++
		}
		if c.logger != nil {
			c.logger.WithContext(ctx).With("consumer", instance, "attempt", attempt+1, "error", err).Warn("Reconnecting the Kafka consumer after a connection failure")
		}
		if err := backoff.Wait(ctx, attempt); err != nil {
			return err
		}
		attempt++
		// 新组首批提交可能尚未成功。只有缺少已提交位点时才保守从保留历史开始，
		// 避免再次应用 StartLatest 跳过已经投递但未确认的记录。
		recoveryOptions = []kgo.Opt{kgo.ConsumeStartOffset(kgo.NewOffset().AtStart())}
	}
}

// consumeClient 运行一次客户端会话；报告是否提交过消息，以便恢复后重置退避。
func (c *consumer) consumeClient(
	ctx context.Context,
	instance string,
	handler DeliveryHandler,
	options ...kgo.Opt,
) (progressed bool, err error) {
	// Poll 受 worker 取消控制；SDK 保持独立生命期，留出向 Broker 正常退组的时间。
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	client, err := c.createClient(sessionCtx, c.config.Connection, instance, options...)
	if err != nil {
		return false, &consumerOperationError{fmt.Errorf("create Kafka consumer %q: %w", instance, err)}
	}
	if client == nil {
		return false, fmt.Errorf("create Kafka consumer %q: manager returned nil", instance)
	}
	defer func() {
		// LeaveGroup 也触发再均衡；先释放 Poll 屏障，并限制断网时的退出等待。
		client.AllowRebalance()
		leaveCtx, stopLeave := context.WithTimeout(context.WithoutCancel(ctx), leaveGroupTimeout)
		leaveErr := client.LeaveGroupContext(leaveCtx)
		stopLeave()
		cancel()
		client.CloseAllowingRebalance()
		if leaveErr != nil && c.logger != nil {
			c.logger.WithContext(leaveCtx).With("consumer", instance, "error", leaveErr).Warn("Failed to leave the Kafka consumer group")
		}
	}()

	if c.logger != nil {
		c.logger.WithContext(ctx).With("destination", c.config.Topic, "group", c.config.Group, "consumer", instance).Debug("Kafka consumer is ready")
	}
	for {
		fetches := client.PollRecords(ctx, c.config.MaxPollRecords)
		c.logFetchEvents(ctx, instance, fetches)
		err := processFetches(ctx, client, fetches, handler)
		client.AllowRebalance()
		if err != nil {
			return progressed, err
		}
		if fetches.NumRecords() > 0 {
			progressed = true
		}
	}
}

func consumerInstance(config ConsumerConfig, index int) string {
	if config.Concurrency <= 1 {
		return config.Instance
	}
	return fmt.Sprintf("%s-%d", config.Instance, index+1)
}
