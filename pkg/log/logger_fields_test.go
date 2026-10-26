package log

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
)

func TestCurrentCacheFollowsSharedVersion(t *testing.T) {
	shared := &sharedState{}
	old := &customState{version: 1}
	current := &customState{version: 2, filterKeys: []string{"secret"}}
	shared.custom.Store(current)
	l := &logger{shared: shared, config: &configState{msgKey: defaultMsgKey}}
	first := l.currentCache(current)
	if first.customVersion != current.version || !first.filter.drops("secret") {
		t.Fatalf("cache = %+v", first)
	}
	if l.currentCache(current) != first {
		t.Fatal("same version rebuilt cache")
	}
	// 旧快照构造的缓存不会被新版本复用。
	stale := l.currentCache(old)
	if stale.customVersion != old.version || stale.filter.drops("secret") {
		t.Fatalf("stale cache = %+v", stale)
	}
	if again := l.currentCache(current); again.customVersion != current.version || !again.filter.drops("secret") {
		t.Fatalf("current cache after stale = %+v", again)
	}
}

// referencePipeline 是改造前逐层包装的字段处理链，作为融合实现的等价性基准。
func referencePipeline(fields []any, module string, filterKeys []string, freeze, filterEmpty bool) []any {
	fields = append([]any(nil), fields...)
	if freeze {
		for i, value := range fields {
			if _, plain := value.(string); plain || value == nil {
				continue
			}
			format := "%v"
			if i%2 == 0 {
				format = "%s"
			}
			fields[i] = &referenceFormatted{value: value, format: format}
		}
	}
	var event, ready referenceSink
	filtered := output.NewFilter(&event, false, output.FilterKeysSet(filterKeys))
	_ = output.NewModule(filtered, module).Log(kratoslog.LevelInfo, fields...)
	values := []any(event)
	if freeze {
		frozen := make([]any, len(values))
		for i, value := range values {
			if i%2 == 0 {
				if key, ok := value.(string); ok {
					frozen[i] = key
				} else {
					frozen[i] = formattedKey(fmt.Sprintf("%s", value))
				}
			} else if value != nil {
				frozen[i] = fmt.Sprintf("%v", value)
			}
		}
		values = frozen
	}
	_ = output.NewFilter(&ready, filterEmpty, nil).Log(kratoslog.LevelInfo, values...)
	return referenceDedupe(ready)
}

// referenceDedupe 是改造前的去重规则：重复字符串 key 保留首次位置、取最后一个值。
func referenceDedupe(keyvals []any) []any {
	result := make([]any, 0, len(keyvals))
	positions := make(map[string]int, len(keyvals)/2)
	for index := 0; index < len(keyvals); index += 2 {
		if index+1 == len(keyvals) {
			result = append(result, keyvals[index])
			break
		}
		key, ok := keyvals[index].(string)
		if !ok {
			result = append(result, keyvals[index], keyvals[index+1])
			continue
		}
		if position, exists := positions[key]; exists {
			result[position+1] = keyvals[index+1]
			continue
		}
		positions[key] = len(result)
		result = append(result, key, keyvals[index+1])
	}
	return result
}

type referenceSink []any

func (s *referenceSink) Log(_ kratoslog.Level, fields ...any) error {
	*s = append([]any(nil), fields...)
	return nil
}

type referenceFormatted struct {
	value  any
	format string
}

func (f *referenceFormatted) String() string { return fmt.Sprintf(f.format, f.value) }

type namedString string

type renderStringer struct{ text string }

func (s renderStringer) String() string { return s.text }

func TestRenderMatchesReferencePipeline(t *testing.T) {
	cases := []struct {
		name       string
		fields     []any
		module     string
		filterKeys []string
	}{
		{name: "ordering", fields: []any{"a", 1, "caller", "x.go:1", "ts", "now", "b", 2}},
		{name: "fixed module overrides", fields: []any{"module", "other", "a", 1}, module: "fixed"},
		{name: "last valid module", fields: []any{"module", "first", "module", " bad", "module", "last", "module", 7}},
		{name: "missing module", fields: []any{"a", 1}},
		{name: "odd trailing key", fields: []any{"a", 1, "orphan"}},
		{name: "trailing module key", fields: []any{"a", 1, "module"}},
		{name: "duplicates last wins", fields: []any{"k", 1, "x", 2, "k", 3, "ts", "a", "ts", "b"}},
		{name: "non-string keys", fields: []any{7, "seven", 7, "again", nil, "nil-key", renderStringer{"s"}, "v"}},
		{name: "named string key", fields: []any{namedString("n"), 1, "n", 2}},
		{name: "exact and prefix filters", fields: []any{"secret", "x", "token.a", "y", "tokenizer", "z", "module", "m", "keep", 1}, filterKeys: []string{"secret", "token*", "module"}},
		{name: "filter all", fields: []any{"a", 1, "b", 2}, filterKeys: []string{"*"}},
		{name: "empty and zero values", fields: []any{"empty", "", "nil", nil, "zero", 0, "false", false, "stringer", renderStringer{""}}},
		{name: "errors and structs", fields: []any{"error", errors.New("boom"), "map", map[string]int{"a": 1}, "slice", []int{1, 2}}},
		{name: "misaligned from odd prefix", fields: []any{"lonely", "a", 1, "b", nil, 3}},
		{name: "empty", fields: nil},
	}
	for _, test := range cases {
		for _, freeze := range []bool{true, false} {
			for _, filterEmpty := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/freeze=%v/empty=%v", test.name, freeze, filterEmpty), func(t *testing.T) {
					assertRenderMatchesReference(t, test.fields, test.module, test.filterKeys, freeze, filterEmpty)
				})
			}
		}
	}
}

// 随机组合 key、值类型和过滤规则，覆盖手写用例遗漏的交错情况。
func TestRenderMatchesReferencePipelineRandomized(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	keys := []any{"ts", "caller", "module", "a", "b", "secret", "token.x", 7, nil, namedString("a")}
	values := []any{"", "v", " v", 0, 1, false, nil, renderStringer{""}, renderStringer{"s"}, errors.New("e"), []int{1}}
	filterSets := [][]string{nil, {"secret"}, {"token*"}, {"a", "module"}, {"*"}}
	for iteration := range 2000 {
		fields := make([]any, random.IntN(14))
		for i := range fields {
			if i%2 == 0 {
				fields[i] = keys[random.IntN(len(keys))]
			} else {
				fields[i] = values[random.IntN(len(values))]
			}
		}
		module := ""
		if random.IntN(3) == 0 {
			module = "fixed"
		}
		filterKeys := filterSets[random.IntN(len(filterSets))]
		freeze, filterEmpty := random.IntN(2) == 0, random.IntN(2) == 0
		if !assertRenderMatchesReference(t, fields, module, filterKeys, freeze, filterEmpty) {
			t.Fatalf("iteration %d diverged", iteration)
		}
	}
}

func assertRenderMatchesReference(t *testing.T, fields []any, module string, filterKeys []string, freeze, filterEmpty bool) bool {
	t.Helper()
	want := referencePipeline(fields, module, filterKeys, freeze, filterEmpty)
	cache := &loggerCache{filter: newKeyFilter(filterKeys)}
	if module != "" {
		cache.module = module
	}
	var texts []any
	if freeze {
		texts = make([]any, len(fields))
	}
	got := cache.render(fields, freeze, filterEmpty, texts)
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields=%#v module=%q filters=%v freeze=%v empty=%v\n got=%#v\nwant=%#v", fields, module, filterKeys, freeze, filterEmpty, got, want)
		return false
	}
	return true
}

// 策略重试复用已格式化文本：格式化方法每个字段最多调用一次，被过滤字段从不调用。
func TestRenderFormatsEachKeptFieldOnce(t *testing.T) {
	calls := map[string]int{}
	fields := []any{"kept", countingStringer{name: "kept", calls: calls}, "secret", countingStringer{name: "secret", calls: calls}}
	texts := make([]any, len(fields))
	for _, filters := range [][]string{nil, {"secret"}, {"secret"}} {
		cache := &loggerCache{filter: newKeyFilter(filters)}
		cache.render(fields, true, false, texts)
	}
	if calls["kept"] != 1 {
		t.Fatalf("kept field formatted %d times", calls["kept"])
	}
	// 第一轮未过滤时 secret 被格式化一次；之后命中过滤不再格式化。
	if calls["secret"] != 1 {
		t.Fatalf("secret field formatted %d times", calls["secret"])
	}
	calls = map[string]int{}
	fields = []any{"secret", countingStringer{name: "secret", calls: calls}}
	(&loggerCache{filter: newKeyFilter([]string{"secret"})}).render(fields, true, false, make([]any, len(fields)))
	if calls["secret"] != 0 {
		t.Fatal("filtered field was formatted")
	}
}

type countingStringer struct {
	name  string
	calls map[string]int
}

func (s countingStringer) String() string {
	s.calls[s.name]++
	return s.name
}
