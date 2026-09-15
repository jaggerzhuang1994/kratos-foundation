package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// logFetchEvents 只记录 Kafka 协议级的数据丢失与 Group Session 事件。
func (c *consumer) logFetchEvents(instance string, fetches kgo.Fetches) {
	if c.logger == nil {
		return
	}
	for _, fetchError := range fetches.Errors() {
		err := fmt.Errorf(
			"kafka fetch %s[%d]: %w",
			fetchError.Topic,
			fetchError.Partition,
			fetchError.Err,
		)
		var dataLoss *kgo.ErrDataLoss
		var groupSession *kgo.ErrGroupSession
		switch {
		case errors.As(fetchError.Err, &dataLoss):
			c.logger.With("connection", c.config.Connection, "group", c.config.Group, "consumer", instance, "topic", fetchError.Topic, "partition", fetchError.Partition, "error", err).Error("Kafka reported data loss while fetching records")
		case errors.As(fetchError.Err, &groupSession):
			c.logger.With("connection", c.config.Connection, "group", c.config.Group, "consumer", instance, "topic", fetchError.Topic, "partition", fetchError.Partition, "error", err).Warn("Kafka consumer group session was lost")
		}
	}
}

// processFetches 顺序投递真实 Fetch Record，只有整批 Handler 成功后才提交全部位点。
// Fetch、Context、Handler 或 Commit 失败时保留原错误链并且不会提前提交。
func processFetches(
	ctx context.Context,
	committer recordCommitter,
	fetches kgo.Fetches,
	handler DeliveryHandler,
) error {
	if handler == nil {
		return errors.New("kafka queue delivery handler is nil")
	}
	if committer == nil {
		return errors.New("kafka record committer is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := classifyFetchErrors(ctx, fetches.Errors()); err != nil {
		return &consumerOperationError{err}
	}

	// 单分区是小批次的常见形状，直接借用 SDK 的记录切片，避免仅为展平再分配。
	// 不修改该切片；多分区仍沿用 SDK 的顺序和完整提交列表。
	var records []*kgo.Record
	if len(fetches) == 1 && len(fetches[0].Topics) == 1 && len(fetches[0].Topics[0].Partitions) == 1 {
		records = fetches[0].Topics[0].Partitions[0].Records
	} else {
		records = fetches.Records()
	}
	for _, record := range records {
		delivery := decodeRecord(record)
		if err := handler(ctx, delivery); err != nil {
			return err
		}
		// Handler 可能自行触发取消；提交前必须重新确认整批仍处于有效生命周期。
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if len(records) == 0 {
		return nil
	}
	if err := committer.CommitRecords(ctx, records...); err != nil {
		return &consumerOperationError{fmt.Errorf("commit Kafka records: %w", err)}
	}
	return nil
}

// classifyFetchErrors 保留 franz-go 对可继续协议事件与致命 Fetch 错误的分类。
func classifyFetchErrors(ctx context.Context, fetchErrors []kgo.FetchError) error {
	errorsList := make([]error, 0, len(fetchErrors))
	for _, fetchError := range fetchErrors {
		err := fmt.Errorf(
			"kafka fetch %s[%d]: %w",
			fetchError.Topic,
			fetchError.Partition,
			fetchError.Err,
		)
		var dataLoss *kgo.ErrDataLoss
		var groupSession *kgo.ErrGroupSession
		switch {
		case errors.As(fetchError.Err, &dataLoss):
			// franz-go 已重置到可继续位置；Client 无需因协议级数据丢失事件重建。
		case errors.As(fetchError.Err, &groupSession):
			// franz-go 自动恢复暂时会话失败；永久错误也可能是 TLS/首读错误，
			// 不能只检查 Kafka 协议错误；首读 EOF 交给外层有次数上限的重建路径。SDK 自己取消旧会话仍可继续。
			// 只放行单一取消链，不能让聚合错误中的取消掩盖另一项永久故障。
			cause := groupSession.Err
			for errors.Unwrap(cause) != nil {
				cause = errors.Unwrap(cause)
			}
			if cause == context.Canceled {
				if ctx.Err() != nil {
					return ctx.Err()
				}
			} else if !transientKafkaError(groupSession.Err, false) {
				errorsList = append(errorsList, err)
			}
		case errors.Is(fetchError.Err, context.Canceled),
			errors.Is(fetchError.Err, context.DeadlineExceeded):
			if ctx.Err() != nil {
				return ctx.Err()
			}
		case errors.Is(fetchError.Err, kgo.ErrClientClosed):
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errorsList = append(errorsList, err)
		default:
			errorsList = append(errorsList, err)
		}
	}
	if len(errorsList) > 0 {
		return errors.Join(errorsList...)
	}
	return nil
}
