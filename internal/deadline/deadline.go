package deadline

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type policy struct {
	maxTimeout      time.Duration
	minBudget       time.Duration
	fallbackTimeout time.Duration
}

func (p policy) validate() error {
	switch {
	case p.maxTimeout < 0:
		return fmt.Errorf(
			"%w: max_timeout must not be negative: %s",
			ErrInvalidPolicy,
			p.maxTimeout,
		)
	case p.minBudget < 0:
		return fmt.Errorf(
			"%w: min_budget must not be negative: %s",
			ErrInvalidPolicy,
			p.minBudget,
		)
	case p.fallbackTimeout < 0:
		return fmt.Errorf(
			"%w: fallback_timeout must not be negative: %s",
			ErrInvalidPolicy,
			p.fallbackTimeout,
		)
	default:
		return nil
	}
}

// Derive 根据 operation 对应的策略创建下游 Context。
//
// 它在同一个配置快照上完成路由选择、最终截止时间计算、最小预算检查和决策信息写入。
// 调用方在 err == nil 时必须调用 cancel。
func (s *Store) Derive(
	parent context.Context,
	operation string,
) (context.Context, context.CancelFunc, error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}

	selected := s.resolve(operation)
	now := time.Now()
	parentDeadline, hasParentDeadline := parent.Deadline()
	if !hasParentDeadline &&
		selected.fallbackTimeout == 0 &&
		selected.maxTimeout == 0 {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}

	effectiveDeadline, source, err := calculateEffectiveDeadline(
		now,
		parentDeadline,
		hasParentDeadline,
		selected,
	)
	if err != nil {
		return nil, nil, err
	}

	effectiveRemaining := effectiveDeadline.Sub(now)
	if effectiveRemaining <= 0 {
		return nil, nil, context.DeadlineExceeded
	}
	if effectiveRemaining < selected.minBudget {
		return nil, nil, &InsufficientBudgetError{
			Remaining: effectiveRemaining,
			Required:  selected.minBudget,
		}
	}

	deadlineCtx, cancel := context.WithDeadline(parent, effectiveDeadline)
	ctx := withInfo(deadlineCtx, Info{
		Operation:         operation,
		AppliedAt:         now,
		HasParentDeadline: hasParentDeadline,
		ParentDeadline:    parentDeadline,
		EffectiveDeadline: effectiveDeadline,
		Source:            source,
		RemainingAtApply:  effectiveRemaining,
		MinBudget:         selected.minBudget,
		MaxTimeout:        selected.maxTimeout,
		FallbackTimeout:   selected.fallbackTimeout,
	})
	return ctx, cancel, nil
}

func calculateEffectiveDeadline(
	now time.Time,
	parentDeadline time.Time,
	hasParentDeadline bool,
	selected policy,
) (time.Time, Source, error) {
	if hasParentDeadline {
		remaining := parentDeadline.Sub(now)
		if remaining <= 0 {
			return time.Time{}, "", context.DeadlineExceeded
		}

		effectiveDeadline := parentDeadline
		source := SourceParent
		if selected.maxTimeout > 0 {
			localDeadline := now.Add(selected.maxTimeout)
			if localDeadline.Before(effectiveDeadline) {
				effectiveDeadline = localDeadline
				source = SourceMax
			}
		}
		return effectiveDeadline, source, nil
	}

	timeout := selected.fallbackTimeout
	source := SourceFallback
	if timeout == 0 ||
		(selected.maxTimeout > 0 && selected.maxTimeout < timeout) {
		timeout = selected.maxTimeout
		source = SourceMax
	}
	return now.Add(timeout), source, nil
}

// Source 描述最终 Deadline 的主要限制来源。
type Source string

const (
	// SourceParent 表示直接沿用上游 Deadline。
	SourceParent Source = "parent"
	// SourceMax 表示最终 Deadline 由 max_timeout 决定。
	SourceMax Source = "max_timeout"
	// SourceFallback 表示最终 Deadline 由 fallback_timeout 决定。
	SourceFallback Source = "fallback_timeout"
)

// Info 记录应用 Deadline 策略时的决策信息。
type Info struct {
	// Operation 是本次派生用于选择策略的路由名称。
	Operation string

	AppliedAt time.Time

	// 上游 Deadline。
	HasParentDeadline bool
	ParentDeadline    time.Time

	// 最终实际生效的 Deadline。
	EffectiveDeadline time.Time
	Source            Source

	// 应用策略时的最终预算快照。
	RemainingAtApply time.Duration

	// 应用策略时的配置快照。
	MinBudget       time.Duration
	MaxTimeout      time.Duration
	FallbackTimeout time.Duration
}

// RemainingAt 返回指定时间点的剩余预算。
//
// Deadline 已经过期时可能返回负数。
func (i Info) RemainingAt(now time.Time) time.Duration {
	return i.EffectiveDeadline.Sub(now)
}

// Remaining 返回当前剩余预算。
//
// Deadline 已经过期时可能返回负数。
func (i Info) Remaining() time.Duration {
	return time.Until(i.EffectiveDeadline)
}

// RemainingNonNegative 返回不小于零的当前剩余预算。
//
// 适合用于指标或不接受负 duration 的调用方。
func (i Info) RemainingNonNegative() time.Duration {
	remaining := i.Remaining()
	if remaining < 0 {
		return 0
	}
	return remaining
}

type infoKey struct{}

func withInfo(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, infoKey{}, info)
}

// InfoFromContext 从 Context 中读取 Deadline 决策信息。
func InfoFromContext(ctx context.Context) (Info, bool) {
	info, ok := ctx.Value(infoKey{}).(Info)
	return info, ok
}

var (
	// ErrInsufficientBudget 表示最终剩余预算不足，不应发起下游请求。
	//
	// InsufficientBudgetError 同时兼容：
	//   errors.Is(err, ErrInsufficientBudget)
	//   errors.Is(err, context.DeadlineExceeded)
	ErrInsufficientBudget = errors.New("insufficient deadline budget")

	// ErrInvalidPolicy 表示 deadline 策略配置无效。
	ErrInvalidPolicy = errors.New("invalid deadline policy")
)

// InsufficientBudgetError 表示最终实际可用预算小于 MinBudget。
type InsufficientBudgetError struct {
	Remaining time.Duration
	Required  time.Duration
}

func (e *InsufficientBudgetError) Error() string {
	return fmt.Sprintf(
		"%v: remaining=%s required=%s",
		ErrInsufficientBudget,
		e.Remaining,
		e.Required,
	)
}

// Unwrap 使预算不足同时具备 deadline exceeded 语义。
//
// 因此以下两个判断都会返回 true：
//
//	errors.Is(err, ErrInsufficientBudget)
//	errors.Is(err, context.DeadlineExceeded)
func (e *InsufficientBudgetError) Unwrap() error {
	return context.DeadlineExceeded
}

func (e *InsufficientBudgetError) Is(target error) bool {
	return target == ErrInsufficientBudget
}
