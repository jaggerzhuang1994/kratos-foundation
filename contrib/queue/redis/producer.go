package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

type producer struct {
	client *goredis.Client
	config ProducerConfig
}

// Publish 向配置的 Redis Stream 追加一条消息。
func (p *producer) Publish(ctx context.Context, message *queue.Message) error {
	if message == nil {
		return fmt.Errorf("redis queue message is nil")
	}
	if p == nil || p.client == nil {
		return errors.New("redis queue producer is not initialized")
	}
	_, err := p.client.XAdd(ctx, newXAddArgs(p.config, message)).Result()
	if err != nil {
		return fmt.Errorf("redis XADD %q: %w", p.config.Stream, err)
	}
	return nil
}

// PublishBatch 通过单个 Pipeline 追加多条消息，并以 queue.BatchError 报告逐条失败。
func (p *producer) PublishBatch(ctx context.Context, messages []*queue.Message) error {
	if p == nil || p.client == nil {
		return errors.New("redis queue producer is not initialized")
	}
	if len(messages) == 0 {
		return nil
	}
	for index, message := range messages {
		if message == nil {
			return fmt.Errorf("redis queue message %d is nil", index)
		}
	}
	commands, pipelineErr := p.client.Pipelined(ctx, func(pipe goredis.Pipeliner) error {
		for _, message := range messages {
			pipe.XAdd(ctx, newXAddArgs(p.config, message))
		}
		return nil
	})
	failures, err := collectBatchFailures(messages, commands, pipelineErr)
	if err != nil {
		return err
	}
	if len(failures) > 0 {
		return &queue.BatchError{Failures: failures}
	}
	return nil
}

// collectBatchFailures 按 Pipeline 入队顺序对齐 Redis Command 与输入消息，并在库契约异常时返回可诊断错误而非索引越界。
func collectBatchFailures(
	messages []*queue.Message,
	commands []goredis.Cmder,
	pipelineErr error,
) ([]queue.BatchFailure, error) {
	if len(commands) != len(messages) {
		return nil, fmt.Errorf(
			"redis pipeline returned %d commands for %d messages",
			len(commands),
			len(messages),
		)
	}
	failures := make([]queue.BatchFailure, 0)
	for index, command := range commands {
		if command == nil {
			return nil, fmt.Errorf("redis pipeline command %d is nil", index)
		}
		if err := command.Err(); err != nil {
			failures = append(failures, queue.BatchFailure{
				Index:     index,
				MessageID: messages[index].ID,
				Err:       err,
			})
		}
	}
	if pipelineErr != nil && len(failures) == 0 {
		// go-redis 通常将第一个 Command 错误同时作为 Pipeline 错误；仅在没有逐条错误时才需将全局错误分配给全部输入。
		for index, message := range messages {
			failures = append(failures, queue.BatchFailure{
				Index:     index,
				MessageID: message.ID,
				Err:       pipelineErr,
			})
		}
	}
	return failures, nil
}
