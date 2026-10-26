package log

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

func TestAutomaticCallerPreservesOrdinaryFunctions(t *testing.T) {
	_, _, line, _ := runtime.Caller(0)
	got := caller(1)(context.Background())
	if want := fmt.Sprintf("log/caller_test.go:%d", line+1); got != want {
		t.Fatalf("caller=%v, want %s", got, want)
	}
}

func TestCallerWrapperMatchesOnlyKnownForwarders(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/kafka.(*loggerAdapter).Log", true},
		{"github.com/twmb/franz-go/pkg/kgo.(*wrappedLogger).Log", true},
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job.(*cronLogger).Error", true},
		{"github.com/go-kratos/kratos/v2/log.(*Helper).Errorf", true},
		{"github.com/go-kratos/kratos/v2/log.Info", true},
		{"github.com/go-kratos/kratos/v2/log.businessEvent", false},
		{"example.com/business/log.Info", false},
		{"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job.warnConcurrent", false},
		{"github.com/go-kratos/kratos/v2/middleware/logging.Server.func1.1", false},
		{"github.com/twmb/franz-go/pkg/kgo.(*Client).Produce", false},
	} {
		if got := callerWrapper(tc.name); got != tc.want {
			t.Errorf("%s=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCallerBeyondAvailableFramesIsUnknown(t *testing.T) {
	if got := caller(1000)(context.Background()); got != "unknown" {
		t.Fatalf("caller=%v, want unknown", got)
	}
}

// captureCaller 返回只记录 caller 字段的内置输出 Logger；nested 输出到同一记录器。
func captureCaller() (Logger, *[]any) {
	shared := &sharedState{}
	shared.custom.Store(&customState{})
	var mu sync.Mutex
	var callers []any
	l := &logger{shared: shared, config: &configState{msgKey: defaultMsgKey, output: &outputLogger{preparedOutput: &preparedOutput{output: loggerFunc(func(_ kratoslog.Level, kv ...any) error {
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i] == CallerKey {
				mu.Lock()
				callers = append(callers, kv[i+1])
				mu.Unlock()
			}
		}
		return nil
	})}}}}
	return l, &callers
}

func callerLine(offset int) string {
	_, _, line, _ := runtime.Caller(1)
	return fmt.Sprintf("log/caller_test.go:%d", line+offset)
}

// 同一调用点重复命中 PC 缓存，不同调用点各自定位；缓存不会串用位置。
func TestCallerCacheKeepsEachCallSite(t *testing.T) {
	l, callers := captureCaller()
	var want []string
	for range 3 {
		want = append(want, callerLine(1))
		l.Info("first")
		want = append(want, callerLine(1))
		l.Info("second")
	}
	assertCallers(t, *callers, want)
}

// 字段格式化或求值期间嵌套记录日志时，内外两层各自指向自己的调用点。
func TestCallerWithNestedLogging(t *testing.T) {
	l, callers := captureCaller()
	var innerLine string
	inner := kratoslog.Valuer(func(context.Context) any {
		innerLine = callerLine(1)
		l.Info("inner")
		return "value"
	})
	stringer := callbackLogValue{run: func() { l.WithModule("nested").Infow("msg", "inner stringer") }}
	stringerLine := callerLine(-1)
	outerLine := callerLine(1)
	l.With("valuer", inner).Infow("msg", "outer", "stringer", stringer)
	// 求值先于格式化：Valuer 的嵌套日志先写出，随后是 Stringer 的嵌套日志，最后是外层事件。
	assertCallers(t, *callers, []string{innerLine, stringerLine, outerLine})
}

//go:noinline
func logThroughHelper(l Logger) string {
	line := callerLine(1)
	l.Info("helper")
	return line
}

// inlineableLog 体积很小，通常被内联；内联帧经 CallersFrames 展开后仍定位到函数内的源码行。
func inlineableLog(l Logger) { l.Info("inlined") }

func TestCallerThroughHelpersAndInlinedFrames(t *testing.T) {
	l, callers := captureCaller()
	helperLine := logThroughHelper(l)
	inlinedLine := fmt.Sprintf("log/caller_test.go:%d", inlineableLogLine())
	inlineableLog(l)
	outerLine := callerLine(1)
	logThroughHelper(l.WithCallerDepth(2))
	assertCallers(t, *callers, []string{helperLine, inlinedLine, outerLine})
}

func inlineableLogLine() int {
	pc := reflect.ValueOf(inlineableLog).Pointer()
	_, line := runtime.FuncForPC(pc).FileLine(pc)
	return line
}

//go:noinline
func recurseThenLog(l Logger, remaining int) {
	if remaining == 0 {
		l.Info("deep")
		return
	}
	recurseThenLog(l, remaining-1)
}

// 指定深度超过首轮短扫描时回退完整扫描，结果与逐帧扫描一致。
func TestCallerDepthBeyondShortScan(t *testing.T) {
	l, callers := captureCaller()
	pc := reflect.ValueOf(recurseThenLog).Pointer()
	_, start := runtime.FuncForPC(pc).FileLine(pc)
	// 函数入口地址对应声明行："l.Info" 在其后第 2 行，递归调用在第 5 行。
	leaf, recursive := fmt.Sprintf("log/caller_test.go:%d", start+2), fmt.Sprintf("log/caller_test.go:%d", start+5)
	for _, depth := range []int{1, 2, callerScanShort, callerScanShort + 5} {
		*callers = nil
		recurseThenLog(l.WithCallerDepth(depth), callerScanLimit-10)
		want := recursive
		if depth == 1 {
			want = leaf
		}
		assertCallers(t, *callers, []string{want})
	}
}

// 并发写入不同调用点时，PC 缓存的并发填充不会让调用点互相覆盖。
func TestCallerConcurrentCallSites(t *testing.T) {
	l, callers := captureCaller()
	firstLine, secondLine := callerLine(5), callerLine(7)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				l.Info("first")
				runtime.Gosched()
				l.Info("second")
			}
		})
	}
	wg.Wait()
	counts := map[any]int{}
	for _, value := range *callers {
		counts[value]++
	}
	if counts[firstLine] != 400 || counts[secondLine] != 400 || len(counts) != 2 {
		t.Fatalf("callers = %v, want %s and %s 400 times each", counts, firstLine, secondLine)
	}
}

func assertCallers(t *testing.T, got []any, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("callers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("caller[%d] = %v, want %s (all %v)", i, got[i], want[i], got)
		}
	}
}
