package errtree

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// multiError 模拟保留 nil 子错误的自定义聚合，标准 errors.Join 会提前剔除 nil。
type multiError []error

func (m multiError) Error() string   { return "multi" }
func (m multiError) Unwrap() []error { return m }

// canceledAlias 在中间节点声明取消语义，验证单链按 errors.Is 标准语义判断。
type canceledAlias struct{ cause error }

func (e canceledAlias) Error() string        { return "alias" }
func (e canceledAlias) Unwrap() error        { return e.cause }
func (e canceledAlias) Is(target error) bool { return target == context.Canceled }

func TestOnly(t *testing.T) {
	failure := errors.New("failure")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "leaf", err: context.Canceled, want: true},
		{name: "other leaf", err: failure, want: false},
		{name: "wrapped leaf", err: fmt.Errorf("stop: %w", context.Canceled), want: true},
		{name: "join all match", err: errors.Join(context.Canceled, fmt.Errorf("x: %w", context.Canceled)), want: true},
		{name: "join with failure", err: errors.Join(context.Canceled, failure), want: false},
		{name: "wrapped join with failure", err: fmt.Errorf("x: %w", errors.Join(context.Canceled, failure)), want: false},
		{name: "nested join", err: errors.Join(errors.Join(context.Canceled), fmt.Errorf("y: %w", errors.Join(context.Canceled))), want: true},
		{name: "nil children skipped", err: multiError{nil, context.Canceled, nil}, want: true},
		{name: "only nil children", err: multiError{nil, nil}, want: false},
		{name: "empty aggregate", err: multiError{}, want: false},
		{name: "intermediate Is", err: canceledAlias{cause: failure}, want: true},
		{name: "multi-wrap %w", err: fmt.Errorf("%w; %w", context.Canceled, failure), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Only(test.err, context.Canceled); got != test.want {
				t.Fatalf("Only(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestEveryCallsMatchOncePerBranch(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	var seen []error
	ok := Every(fmt.Errorf("top: %w", errors.Join(first, second)), func(err error) bool {
		seen = append(seen, err)
		return true
	})
	if !ok || len(seen) != 2 || seen[0] != first || seen[1] != second {
		t.Fatalf("Every() = %v, seen %v", ok, seen)
	}
	// 单链只在链顶判断一次，让 match 自己沿链查找。
	seen = nil
	top := fmt.Errorf("top: %w", first)
	if !Every(top, func(err error) bool { seen = append(seen, err); return true }) || len(seen) != 1 || seen[0] != top {
		t.Fatalf("single chain seen %v", seen)
	}
}
