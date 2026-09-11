package database

import (
	"context"
	"errors"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// Stats 转发可选 Repo 统计能力；不支持时返回错误，不伪装成空队列。
func (s *Store) Stats(ctx context.Context, now time.Time) (queue.Stats, error) {
	provider, ok := s.repo.(queue.StatsProvider)
	if !ok {
		return queue.Stats{}, errors.New("database queue repo does not support stats")
	}
	return provider.Stats(ctx, now)
}
