package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	goredis "github.com/redis/go-redis/v9"
)

// producerCommandHook terminates go-redis command processing inside the test
// process. It exercises the real Client/Pipeline command construction without
// opening a network connection or requiring a Redis server.
type producerCommandHook struct {
	mu                  sync.Mutex
	commands            []goredis.Cmder
	pipelines           [][]goredis.Cmder
	processErr          error
	pipelineErr         error
	pipelineCommandErrs map[int]error
}

func (h *producerCommandHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return next
}

func (h *producerCommandHook) ProcessHook(_ goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, command goredis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.commands = append(h.commands, command)
		if h.processErr != nil {
			return h.processErr
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if result, ok := command.(*goredis.StringCmd); ok {
			result.SetVal("1-0")
		}
		return nil
	}
}

func (h *producerCommandHook) ProcessPipelineHook(
	_ goredis.ProcessPipelineHook,
) goredis.ProcessPipelineHook {
	return func(ctx context.Context, commands []goredis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.pipelines = append(h.pipelines, append([]goredis.Cmder(nil), commands...))
		for index, command := range commands {
			if commandErr := h.pipelineCommandErrs[index]; commandErr != nil {
				command.SetErr(commandErr)
				continue
			}
			if result, ok := command.(*goredis.StringCmd); ok {
				result.SetVal(fmt.Sprintf("1-%d", index))
			}
		}
		if h.pipelineErr != nil {
			return h.pipelineErr
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

func (h *producerCommandHook) snapshot() ([]goredis.Cmder, [][]goredis.Cmder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	commands := append([]goredis.Cmder(nil), h.commands...)
	pipelines := make([][]goredis.Cmder, len(h.pipelines))
	for index := range h.pipelines {
		pipelines[index] = append([]goredis.Cmder(nil), h.pipelines[index]...)
	}
	return commands, pipelines
}

func newHookedProducer(t *testing.T, hook *producerCommandHook) *producer {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: "redis.invalid:6379"})
	client.AddHook(hook)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close test Redis client: %v", err)
		}
	})
	return &producer{
		client: client,
		config: ProducerConfig{
			Connection:           "main",
			Stream:               "orders",
			MaxLength:            100,
			ApproximateMaxLength: true,
		},
	}
}

func assertXAddCommand(
	t *testing.T,
	command goredis.Cmder,
	wantStream string,
	wantOptions []string,
	wantMessage *queue.Message,
) {
	t.Helper()
	args := command.Args()
	if len(args) < 4 || strings.ToLower(stringifyRedisArg(args[0])) != "xadd" ||
		stringifyRedisArg(args[1]) != wantStream {
		t.Fatalf("XADD command args = %#v", args)
	}
	idIndex := -1
	for index := 2; index < len(args); index++ {
		if stringifyRedisArg(args[index]) == "*" {
			idIndex = index
			break
		}
	}
	if idIndex < 0 {
		t.Fatalf("XADD command has no generated ID marker: %#v", args)
	}
	options := make([]string, 0, idIndex-2)
	for _, option := range args[2:idIndex] {
		options = append(options, stringifyRedisArg(option))
	}
	if !reflect.DeepEqual(options, wantOptions) {
		t.Fatalf("XADD options = %#v, want %#v", options, wantOptions)
	}
	fieldArgs := args[idIndex+1:]
	if len(fieldArgs)%2 != 0 {
		t.Fatalf("XADD field args are not pairs: %#v", fieldArgs)
	}
	fields := make(map[string]string, len(fieldArgs)/2)
	for index := 0; index < len(fieldArgs); index += 2 {
		fields[stringifyRedisArg(fieldArgs[index])] = stringifyRedisArg(fieldArgs[index+1])
	}
	if fields[fieldID] != wantMessage.ID || fields[fieldKey] != string(wantMessage.Key) ||
		fields[fieldBody] != string(wantMessage.Body) ||
		fields[fieldTimestamp] != fmt.Sprint(wantMessage.Timestamp.UnixNano()) {
		t.Fatalf("XADD message fields = %#v, want %#v", fields, wantMessage)
	}
	var headers []queue.Header
	if err := json.Unmarshal([]byte(fields[fieldHeaders]), &headers); err != nil {
		t.Fatalf("decode XADD headers %q: %v", fields[fieldHeaders], err)
	}
	if !reflect.DeepEqual(headers, wantMessage.Headers) {
		t.Fatalf("XADD headers = %#v, want %#v", headers, wantMessage.Headers)
	}
}

func stringifyRedisArg(value any) string {
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return fmt.Sprint(value)
}

func assertBatchFailures(t *testing.T, got, want []queue.BatchFailure) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("batch failures = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index].Index != want[index].Index || got[index].MessageID != want[index].MessageID ||
			!errors.Is(got[index].Err, want[index].Err) {
			t.Fatalf("batch failure %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}
