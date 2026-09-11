package deadline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestStoreDeriveSelectsPolicyAndRecordsDecision(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(200 * time.Millisecond),
		MaxTimeout:      durationpb.New(100 * time.Millisecond),
		Routes: []*config_pb.Middleware_Deadline_RouteRule{
			{
				Rule: &config_pb.Middleware_Deadline_RouteRule_Path{
					Path: "/example.Service/Exact",
				},
				MaxTimeout: durationpb.New(50 * time.Millisecond),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel, err := store.Derive(context.Background(), "/example.Service/Exact")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	info, ok := InfoFromContext(ctx)
	if !ok {
		t.Fatal("deadline info is missing")
	}
	if info.Operation != "/example.Service/Exact" {
		t.Fatalf("operation = %q", info.Operation)
	}
	if info.Source != SourceMax || info.MaxTimeout != 50*time.Millisecond {
		t.Fatalf("deadline info = %+v", info)
	}
	if remaining := time.Until(info.EffectiveDeadline); remaining <= 0 || remaining > 50*time.Millisecond {
		t.Fatalf("remaining = %s, want at most 50ms", remaining)
	}
}

func TestStoreDeriveKeepsShorterParentDeadline(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{
		MaxTimeout: durationpb.New(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelParent()

	ctx, cancel, err := store.Derive(parent, "/example.Service/Call")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	info, ok := InfoFromContext(ctx)
	if !ok || info.Source != SourceParent {
		t.Fatalf("deadline info = %+v, present=%v", info, ok)
	}
	if !info.EffectiveDeadline.Equal(info.ParentDeadline) {
		t.Fatalf("effective deadline = %s, parent = %s", info.EffectiveDeadline, info.ParentDeadline)
	}
}

func TestStoreDeriveExplicitZeroFallbackKeepsUnlimitedContext(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel, err := store.Derive(context.Background(), "/example.Service/Call")
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero policy created a deadline")
	}
	if _, ok := InfoFromContext(ctx); ok {
		t.Fatal("zero policy recorded deadline info")
	}
}

func TestStoreDeriveRejectsInsufficientFinalBudget(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{
		MinBudget: durationpb.New(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err = store.Derive(parent, "/example.Service/Call")
	if !errors.Is(err, ErrInsufficientBudget) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestStoreDeriveSelectsRouteAndUpdatesAtomically(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(time.Second),
		Routes: []*config_pb.Middleware_Deadline_RouteRule{
			{
				Rule:            &config_pb.Middleware_Deadline_RouteRule_Prefix{Prefix: "/example."},
				FallbackTimeout: durationpb.New(400 * time.Millisecond),
			},
			{
				Rule:            &config_pb.Middleware_Deadline_RouteRule_Prefix{Prefix: "/example.Service/"},
				FallbackTimeout: durationpb.New(200 * time.Millisecond),
			},
			{
				Rule:            &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/example.Service/Exact"},
				FallbackTimeout: durationpb.New(80 * time.Millisecond),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 保留旧实现的空 operation 边界：选择首个前缀规则。
	if got := deriveInfo(t, store, "").FallbackTimeout; got != 400*time.Millisecond {
		t.Fatalf("empty operation fallback=%v", got)
	}

	exact := deriveInfo(t, store, "/example.Service/Exact")
	if exact.FallbackTimeout != 80*time.Millisecond {
		t.Fatalf("exact fallback = %s, want 80ms", exact.FallbackTimeout)
	}
	prefix := deriveInfo(t, store, "/example.Service/Other")
	if prefix.FallbackTimeout != 200*time.Millisecond {
		t.Fatalf("prefix fallback = %s, want 200ms", prefix.FallbackTimeout)
	}

	if err := store.Update(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(300 * time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	updated := deriveInfo(t, store, "/other.Service/Call")
	if updated.FallbackTimeout != 300*time.Millisecond {
		t.Fatalf("updated fallback = %s, want 300ms", updated.FallbackTimeout)
	}

	if err := store.Update(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(10 * time.Millisecond),
		MinBudget:       durationpb.New(20 * time.Millisecond),
	}); err == nil {
		t.Fatal("invalid update succeeded")
	}
	retained := deriveInfo(t, store, "/other.Service/Call")
	if retained.FallbackTimeout != 300*time.Millisecond {
		t.Fatalf("failed update changed fallback to %s", retained.FallbackTimeout)
	}
}

func TestStoreDeriveRouteCanDisableInheritedLimits(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{
		FallbackTimeout: durationpb.New(time.Second),
		MaxTimeout:      durationpb.New(time.Second),
		MinBudget:       durationpb.New(20 * time.Millisecond),
		Routes: []*config_pb.Middleware_Deadline_RouteRule{
			{
				Rule:            &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/unlimited"},
				FallbackTimeout: durationpb.New(0),
				MaxTimeout:      durationpb.New(0),
				MinBudget:       durationpb.New(0),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel, err := store.Derive(context.Background(), "/unlimited")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero route override created a deadline")
	}
	if _, ok := InfoFromContext(ctx); ok {
		t.Fatal("zero route override recorded deadline info")
	}
}

func TestStoreDeriveReturnsCanceledParentError(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = store.Derive(parent, "/example.Service/Call")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func deriveInfo(t *testing.T, store *Store, operation string) Info {
	t.Helper()
	ctx, cancel, err := store.Derive(context.Background(), operation)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	info, ok := InfoFromContext(ctx)
	if !ok {
		t.Fatal("deadline info is missing")
	}
	return info
}

func TestInfoRemainingAtUsesRequestedInstant(t *testing.T) {
	t.Parallel()

	effective := time.Unix(1_700_000_010, 0)
	info := Info{EffectiveDeadline: effective}
	if got := info.RemainingAt(time.Unix(1_700_000_007, 0)); got != 3*time.Second {
		t.Fatalf("RemainingAt() = %s, want 3s", got)
	}
}

func TestInfoRemainingTracksCurrentTime(t *testing.T) {
	t.Parallel()

	info := Info{EffectiveDeadline: time.Now().Add(time.Minute)}
	upper := time.Until(info.EffectiveDeadline)
	got := info.Remaining()
	lower := time.Until(info.EffectiveDeadline)
	if got > upper || got < lower {
		t.Fatalf("Remaining() = %s, want between %s and %s", got, lower, upper)
	}
}

func TestInfoRemainingNonNegativeClampsExpiredDeadline(t *testing.T) {
	t.Parallel()

	future := Info{EffectiveDeadline: time.Now().Add(time.Minute)}
	if got := future.RemainingNonNegative(); got <= 0 || got > time.Minute {
		t.Fatalf("future RemainingNonNegative() = %s, want within (0, 1m]", got)
	}
	expired := Info{EffectiveDeadline: time.Now().Add(-time.Second)}
	if got := expired.RemainingNonNegative(); got != 0 {
		t.Fatalf("expired RemainingNonNegative() = %s, want 0", got)
	}
}

func TestInsufficientBudgetErrorFormatsBothBudgets(t *testing.T) {
	t.Parallel()

	err := (&InsufficientBudgetError{
		Remaining: 2 * time.Second,
		Required:  5 * time.Second,
	}).Error()
	const want = "insufficient deadline budget: remaining=2s required=5s"
	if err != want {
		t.Fatalf("Error() = %q, want %q", err, want)
	}
}
