package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

// statsScript 原子只读采样现有集合；最多读取 1000 个 Ready 候选的载荷。
// 无需搬迁已到期成员，避免监控改变消费顺序；大队列仍返回精确计数。
const statsScript = validateKeysScript + `
local at = ARGV[1]
local listed = redis.call('LLEN', KEYS[2])
local due = redis.call('ZCOUNT', KEYS[3], '-inf', at)
local expired = redis.call('ZCOUNT', KEYS[4], '-inf', at)
local ready = listed + due + expired
local result = {ready, redis.call('ZCARD', KEYS[3]) - due,
 redis.call('ZCARD', KEYS[4]) - expired, redis.call('ZCARD', KEYS[5])}
if ready > 1000 or ready == 0 then return result end
local ids = redis.call('LRANGE', KEYS[2], 0, 999)
for _, key in ipairs({KEYS[3], KEYS[4]}) do
 for _, id in ipairs(redis.call('ZRANGEBYSCORE', key, '-inf', at, 'LIMIT', 0, 1000)) do
  table.insert(ids, id)
 end
end
for _, id in ipairs(ids) do
 local raw = redis.call('HGET', KEYS[1], id)
 if not raw then return redis.error_reply('missing ready queue record') end
 table.insert(result, raw)
end
return result
`

// Stats 返回精确状态计数；Ready 超过 1000 时年龄未知，不扫描全部载荷。
// OldestReadyAt 使用当前 AvailableAt，与数据库后端一致；不改变任何任务状态。
func (s *Store) Stats(ctx context.Context, now time.Time) (queue.Stats, error) {
	// 借用连接不能改写选项；关闭 Context 超时时拒绝采样，避免指标请求超过其截止时间。
	if !s.client.Options().ContextTimeoutEnabled {
		return queue.Stats{}, errors.New("redis queue stats requires context timeout enabled")
	}
	values, err := s.client.Eval(ctx, statsScript, s.keys, now.UnixMilli()).Slice()
	if err != nil {
		return queue.Stats{}, fmt.Errorf("read redis queue stats: %w", err)
	}
	if len(values) < 4 {
		return queue.Stats{}, errors.New("invalid redis queue stats")
	}
	var counts [4]int64
	for i := range counts {
		value, ok := values[i].(int64)
		if !ok || value < 0 {
			return queue.Stats{}, errors.New("invalid redis queue count")
		}
		counts[i] = value
	}
	result := queue.Stats{Ready: counts[0], Scheduled: counts[1], Running: counts[2], Failed: counts[3]}
	if result.Ready > 1000 {
		return result, nil
	}
	if int64(len(values)-4) != result.Ready {
		return queue.Stats{}, errors.New("incomplete redis queue stats")
	}
	result.OldestReadyKnown = true
	for i, value := range values[4:] {
		raw, ok := value.(string)
		if !ok {
			return queue.Stats{}, errors.New("invalid redis queue stats record")
		}
		var stored record
		var task queue.Task
		if json.Unmarshal([]byte(raw), &stored) != nil || json.Unmarshal([]byte(stored.Task), &task) != nil || strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Type) == "" {
			return queue.Stats{}, errors.New("decode redis queue stats task")
		}
		at := time.UnixMilli(scheduledMillis(task.AvailableAt)).UTC()
		if i == 0 || at.Before(result.OldestReadyAt) {
			result.OldestReadyAt = at
		}
	}
	return result, nil
}
