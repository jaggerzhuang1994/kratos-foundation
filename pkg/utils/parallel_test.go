package utils

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

func TestParallel(t *testing.T) {
	if err := Parallel(); err != nil {
		t.Fatal(err)
	}
	// 各任务独占一个元素，验证 Wait 返回后结果已发布，不引入共享写入。
	result := make([]int, 2)
	if err := Parallel(
		func(context.Context) error { result[0] = 1; return nil },
		func(context.Context) error { result[1] = 2; return nil },
	); err != nil || result[0] != 1 || result[1] != 2 {
		t.Fatal(result, err)
	}
}

func TestParallelWithContext(t *testing.T) {
	want := errors.New("task failed")
	started := make(chan struct{})
	finished := make(chan struct{})
	err := ParallelWithContext(context.Background(),
		func(context.Context) error { <-started; return want },
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(finished)
			return ctx.Err()
		},
	)
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("returned before remaining task finished")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ParallelWithContext(ctx, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestParallelWithLimit(t *testing.T) {
	for _, workers := range []int{1, 2, 10} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const count = 5
				started := make(chan struct{}, count)
				release := make(chan struct{})
				done := make(chan error, 1)
				results := make([]int, count)
				tasks := make([]func(context.Context) error, count)
				for i := range tasks {
					tasks[i] = func(context.Context) error {
						started <- struct{}{}
						<-release
						results[i] = i + 1
						return nil
					}
				}
				go func() { done <- ParallelWithLimit(context.Background(), workers, tasks...) }()
				synctest.Wait()
				if len(started) != min(workers, count) {
					t.Fatalf("started %d tasks before release, limit %d", len(started), workers)
				}
				close(release)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				for i, result := range results {
					if result != i+1 {
						t.Fatalf("task %d result = %d", i, result)
					}
				}
			})
		})
	}
}

func TestParallelWithLimitErrors(t *testing.T) {
	ctx := context.Background()
	for _, workers := range []int{0, -1} {
		if err := ParallelWithLimit(ctx, workers); err == nil {
			t.Fatal("invalid worker count accepted")
		}
	}
	if err := ParallelWithLimit(ctx, 2); err != nil {
		t.Fatal(err)
	}
	want := errors.New("task failed")
	called := false
	err := ParallelWithLimit(ctx, 1,
		func(context.Context) error { return want },
		func(context.Context) error { called = true; return nil },
	)
	if !errors.Is(err, want) || called {
		t.Fatal("failed batch continued", err, called)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ParallelWithLimit(canceled, 1, func(context.Context) error {
		t.Fatal("task executed with canceled context")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestParallelWithLimitCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		skipped := false
		go func() {
			done <- ParallelWithLimit(ctx, 1,
				func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					<-release
					return ctx.Err()
				},
				func(context.Context) error { skipped = true; return nil },
			)
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned before running task exited: %v", err)
		default:
		}
		close(release)
		if err := <-done; !errors.Is(err, context.Canceled) || skipped {
			t.Fatal(err, skipped)
		}
	})
}

func TestParallelMapOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		input := []int{0, 1, 2}
		release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		started := make(chan int, len(input))
		finished := make(chan int, len(input))
		done := make(chan struct{})
		var got []string
		var err error
		go func() {
			got, err = ParallelMap(context.Background(), 2, input, func(_ context.Context, v int) (string, error) {
				started <- v
				<-release[v]
				finished <- v
				return fmt.Sprintf("value-%d", v), nil
			})
			close(done)
		}()
		synctest.Wait()
		if len(started) != 2 {
			t.Fatalf("started %d tasks, want 2", len(started))
		}
		// 强制后续元素先完成，验证结果按输入索引存放，而非按完成顺序追加。
		for _, index := range []int{1, 2, 0} {
			close(release[index])
			if completed := <-finished; completed != index {
				t.Fatalf("completed %d, want %d", completed, index)
			}
		}
		<-done
		if err != nil || !reflect.DeepEqual(got, []string{"value-0", "value-1", "value-2"}) {
			t.Fatal(got, err)
		}
	})
}

func TestParallelMapFailure(t *testing.T) {
	want := errors.New("mapping failed")
	var visited []int
	got, err := ParallelMap(context.Background(), 1, []int{0, 1, 2}, func(_ context.Context, v int) (int, error) {
		visited = append(visited, v)
		if v == 1 {
			return 0, want
		}
		return v + 10, nil
	})
	if got != nil || !errors.Is(err, want) || !strings.Contains(err.Error(), "index 1") ||
		!reflect.DeepEqual(visited, []int{0, 1}) {
		t.Fatal(got, err, visited)
	}
}

func TestParallelMapEmptyAndCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		workers int
		input   []int
		wantErr bool
	}{
		{"empty", context.Background(), 2, nil, false},
		{"invalid limit", context.Background(), 0, []int{1}, true},
		{"canceled", ctx, 2, []int{1}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParallelMap(tt.ctx, tt.workers, tt.input, func(context.Context, int) (int, error) {
				t.Error("unexpected callback")
				return 0, nil
			})
			if tt.wantErr {
				if err == nil || result != nil {
					t.Fatal(result, err)
				}
				if tt.name == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != nil || result == nil || len(result) != 0 {
				t.Fatal(result, err)
			}
		})
	}
}

func TestParallelMapCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		done := make(chan struct{})
		var result []int
		var err error
		go func() {
			result, err = ParallelMap(ctx, 1, []int{0, 1}, func(ctx context.Context, v int) (int, error) {
				if v == 0 {
					return 10, nil
				}
				close(started)
				<-ctx.Done()
				return 0, ctx.Err()
			})
			close(done)
		}()
		<-started
		cancel()
		<-done
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
	})
}
