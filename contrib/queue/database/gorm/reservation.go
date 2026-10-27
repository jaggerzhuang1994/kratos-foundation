package gorm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// claimCandidates 是 SKIP LOCKED 模式一次读取的候选数；并发 Worker 依次锁定其中未被占用的行。
const claimCandidates = 16

// skipLocked 取值：0 未探测，1 支持 SKIP LOCKED，2 不支持（SQLite、MySQL 5.7 等）。
const (
	skipLockedUnknown int32 = iota
	skipLockedSupported
	skipLockedUnsupported
)

// Claim 竞争领取一个到期任务。MySQL 8.0.1+ / MariaDB 10.6+ 在短事务内用 FOR UPDATE SKIP LOCKED
// 逐个锁定候选行，并发 Worker 各自拿到不同任务；其余方言读取快照后条件更新，竞争失败返回空。
// 不跨 Handler 持锁，领取结果在返回前提交。
func (r *Repo[T]) Claim(ctx context.Context, now, until time.Time, token string) (*databasequeue.TaskRecord, error) {
	if token == "" || len(token) > 36 || !until.After(now) {
		return nil, errors.New("invalid queue lease")
	}
	if r.supportsSkipLocked(ctx) {
		return r.claimSkipLocked(ctx, now, until, token)
	}
	entity := r.entity()
	if err := r.available(r.consumerDB(ctx), now).Order("available_at ASC, id ASC").Take(entity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("select queue task: %w", err)
	}
	model := entity.QueueModel()
	record, decodeErr := model.record()
	// Generation 防止同 ID 删除重建的 ABA；Token/Attempts/截止防止覆盖已变化的快照。
	claim := r.consumerDB(ctx).Where("id = ? AND generation = ? AND token = ? AND attempts = ? AND reserved_until = ? AND available_at = ? AND failed = ? AND status = ?", model.ID, model.Generation, model.Token, model.Attempts, model.ReservedUntil, model.AvailableAt, false, model.Status)
	if decodeErr != nil {
		result := claim.Updates(r.quarantineValues(now))
		if result.Error != nil {
			return nil, errors.Join(decodeErr, fmt.Errorf("quarantine queue task: %w", result.Error))
		}
		if result.RowsAffected == 0 {
			return nil, nil
		}
		return nil, decodeErr
	}
	result := claim.Updates(claimValues(model, token, until))
	if result.Error != nil {
		return nil, fmt.Errorf("claim queue task: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return claimedRecord(record, token, until), nil
}

// claimSkipLocked 先无锁读取最早的一批候选，再在短事务内按顺序以主键 FOR UPDATE SKIP LOCKED 锁定，
// 已被其他 Worker 锁定或状态已变化的行直接跳过；主键点查只锁定返回的那一行，不受排序方式影响。
func (r *Repo[T]) claimSkipLocked(ctx context.Context, now, until time.Time, token string) (*databasequeue.TaskRecord, error) {
	var ids []taskID
	if err := r.available(r.consumerDB(ctx), now).Order("available_at ASC, id ASC").Limit(claimCandidates).Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("select queue task: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	base := r.consumerDB(ctx)
	if base.Error != nil {
		return nil, base.Error
	}
	var claimed *databasequeue.TaskRecord
	var decodeErr error
	err := base.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			entity := r.entity()
			locked := r.available(tx.Model(r.entity()).Table(r.table), now).Where("id = ?", id).
				Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate, Options: clause.LockingOptionsSkipLocked})
			if err := locked.Take(entity).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return fmt.Errorf("lock queue task: %w", err)
			}
			model := entity.QueueModel()
			// 行已锁定，按主键和 Generation 更新即可；Generation 仍防止同 ID 重建后的误写。
			update := tx.Model(r.entity()).Table(r.table).Where("id = ? AND generation = ?", model.ID, model.Generation)
			record, err := model.record()
			if err != nil {
				// 损坏正文在同一事务内隔离后提交，再把解码错误返回给 Worker。
				decodeErr = err
				if result := update.Updates(r.quarantineValues(now)); result.Error != nil {
					return fmt.Errorf("quarantine queue task: %w", result.Error)
				}
				return nil
			}
			result := update.Updates(claimValues(model, token, until))
			if result.Error != nil {
				return fmt.Errorf("claim queue task: %w", result.Error)
			}
			if result.RowsAffected == 1 {
				claimed = claimedRecord(record, token, until)
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(decodeErr, err)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return claimed, nil
}

// available 只检索可执行状态，避免大量完成/失败历史参与候选扫描；running 仍按租约截止恢复。
func (r *Repo[T]) available(db *gorm.DB, now time.Time) *gorm.DB {
	return db.Where("status IN ? AND failed = ? AND available_at <= ? AND (reserved_until = 0 OR reserved_until <= ?)", []Status{StatusPending, StatusRunning}, false, timestampFloor(now, r.timestampPrecision), now.UnixMilli())
}

func (r *Repo[T]) quarantineValues(now time.Time) map[string]any {
	return map[string]any{"status": StatusFailed, "failed": true, "failure_reason": queue.FailureCorruptPayload, "failed_at": timestampCeil(now, r.timestampPrecision), "token": "", "reserved_until": 0}
}

func claimValues(model *Model, token string, until time.Time) map[string]any {
	return map[string]any{"status": StatusRunning, "token": token, "reserved_until": deadlineMillis(until), "attempts": model.Attempts + 1}
}

func claimedRecord(record *databasequeue.TaskRecord, token string, until time.Time) *databasequeue.TaskRecord {
	record.Token = token
	record.Attempts++
	record.ReservedUntil = time.UnixMilli(deadlineMillis(until)).UTC()
	return record
}

// supportsSkipLocked 首次领取时读取一次服务端版本；探测失败本次按条件更新处理，下次重试探测。
func (r *Repo[T]) supportsSkipLocked(ctx context.Context) bool {
	switch r.skipLocked.Load() {
	case skipLockedSupported:
		return true
	case skipLockedUnsupported:
		return false
	}
	var version string
	if err := r.consumerDB(ctx).Session(&gorm.Session{NewDB: true}).Raw("SELECT VERSION()").Scan(&version).Error; err != nil {
		return false
	}
	state := skipLockedUnsupported
	if versionSupportsSkipLocked(version) {
		state = skipLockedSupported
	}
	r.skipLocked.Store(state)
	return state == skipLockedSupported
}

// versionSupportsSkipLocked 判断 MySQL 8.0.1+ 或 MariaDB 10.6+；无法解析时按不支持处理。
func versionSupportsSkipLocked(version string) bool {
	mariadb := strings.Contains(strings.ToLower(version), "mariadb")
	parts := strings.SplitN(strings.SplitN(version, "-", 2)[0], ".", 3)
	if len(parts) < 3 {
		return false
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		digits := part
		for j, c := range part {
			if c < '0' || c > '9' {
				digits = part[:j]
				break
			}
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		numbers[i] = n
	}
	minimum := [3]int{8, 0, 1}
	if mariadb {
		minimum = [3]int{10, 6, 0}
	}
	for i := range minimum {
		if numbers[i] != minimum[i] {
			return numbers[i] > minimum[i]
		}
	}
	return true
}
