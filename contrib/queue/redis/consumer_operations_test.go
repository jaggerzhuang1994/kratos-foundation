package redis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestRedisConsumerOperationsIssueExpectedCommandsWithoutServer(t *testing.T) {
	hook := &producerCommandHook{}
	producer := newHookedProducer(t, hook)
	operations := &redisConsumerOperations{client: producer.client}
	ctx := context.Background()

	if err := operations.createGroup(ctx, "orders", "billing", "0-0"); err != nil {
		t.Fatal(err)
	}
	if streams, err := operations.readGroup(ctx, &goredis.XReadGroupArgs{
		Group:    "billing",
		Consumer: "billing-1",
		Streams:  []string{"orders", ">"},
		Count:    2,
		Block:    time.Millisecond,
	}); err != nil || streams != nil {
		t.Fatalf("readGroup() = (%#v, %v)", streams, err)
	}
	if messages, cursor, err := operations.autoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream:   "orders",
		Group:    "billing",
		Consumer: "billing-1",
		MinIdle:  time.Second,
		Start:    "0-0",
		Count:    2,
	}); err != nil || messages != nil || cursor != "" {
		t.Fatalf("autoClaim() = (%#v, %q, %v)", messages, cursor, err)
	}
	if err := operations.acknowledge(ctx, "orders", "billing", "1-0", "1-1"); err != nil {
		t.Fatal(err)
	}
	if ids, err := operations.refreshClaim(ctx, &goredis.XClaimArgs{
		Stream:   "orders",
		Group:    "billing",
		Consumer: "billing-1",
		Messages: []string{"1-0"},
	}); err != nil || ids != nil {
		t.Fatalf("refreshClaim() = (%#v, %v)", ids, err)
	}

	commands, pipelines := hook.snapshot()
	if len(pipelines) != 0 || len(commands) != 5 {
		t.Fatalf("Redis calls = %d commands, %d pipelines", len(commands), len(pipelines))
	}
	wantNames := []string{"xgroup", "xreadgroup", "xautoclaim", "xack", "xclaim"}
	for index, wantName := range wantNames {
		if commands[index].Name() != wantName {
			t.Fatalf("command %d = %#v, want %q", index, commands[index].Args(), wantName)
		}
	}
	assertRedisArgsContain(t, commands[0], "create", "orders", "billing", "0-0", "mkstream")
	assertRedisArgsContain(t, commands[1], "billing", "billing-1", "orders", ">")
	assertRedisArgsContain(t, commands[2], "orders", "billing", "billing-1", "0-0")
	assertRedisArgsContain(t, commands[3], "orders", "billing", "1-0", "1-1")
	assertRedisArgsContain(t, commands[4], "orders", "billing", "billing-1", "1-0", "justid")
}

func TestRedisConsumerOperationsPreserveCommandErrors(t *testing.T) {
	wantErr := errors.New("Redis unavailable")
	hook := &producerCommandHook{processErr: wantErr}
	producer := newHookedProducer(t, hook)
	operations := &redisConsumerOperations{client: producer.client}
	ctx := context.Background()

	if err := operations.createGroup(ctx, "orders", "billing", "0-0"); !errors.Is(err, wantErr) {
		t.Fatalf("createGroup error = %v", err)
	}
	if _, err := operations.readGroup(ctx, &goredis.XReadGroupArgs{
		Group: "billing", Streams: []string{"orders", ">"},
	}); !errors.Is(err, wantErr) {
		t.Fatalf("readGroup error = %v", err)
	}
	if _, _, err := operations.autoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream: "orders", Group: "billing", Consumer: "billing-1", Start: "0-0",
	}); !errors.Is(err, wantErr) {
		t.Fatalf("autoClaim error = %v", err)
	}
	if err := operations.acknowledge(ctx, "orders", "billing", "1-0"); !errors.Is(err, wantErr) {
		t.Fatalf("acknowledge error = %v", err)
	}
	if _, err := operations.refreshClaim(ctx, &goredis.XClaimArgs{
		Stream: "orders", Group: "billing", Consumer: "billing-1", Messages: []string{"1-0"},
	}); !errors.Is(err, wantErr) {
		t.Fatalf("refreshClaim error = %v", err)
	}
}

func TestMalformedMessagePreservesAvailableEvidence(t *testing.T) {
	t.Run("available fields", func(t *testing.T) {
		message := malformedMessage(goredis.XMessage{
			ID: "redis-1",
			Values: map[string]any{
				fieldID:   "application-1",
				fieldKey:  []byte("order-1"),
				fieldBody: "malformed payload",
			},
		})
		if message.ID != "application-1" || string(message.Key) != "order-1" ||
			string(message.Body) != "malformed payload" {
			t.Fatalf("malformed message = %#v", message)
		}
	})

	t.Run("JSON fallback", func(t *testing.T) {
		message := malformedMessage(goredis.XMessage{
			ID: "redis-2",
			Values: map[string]any{
				fieldID:  42,
				"reason": "missing body",
			},
		})
		if message.ID != "redis-2" || !strings.Contains(string(message.Body), "missing body") {
			t.Fatalf("malformed JSON fallback = %#v", message)
		}
	})

	t.Run("text fallback for non-JSON values", func(t *testing.T) {
		message := malformedMessage(goredis.XMessage{
			ID: "redis-3",
			Values: map[string]any{
				"unsupported": make(chan struct{}),
			},
		})
		if message.ID != "redis-3" || !strings.Contains(string(message.Body), "unsupported") {
			t.Fatalf("malformed text fallback = %#v", message)
		}
	})
}

func assertRedisArgsContain(t *testing.T, command goredis.Cmder, values ...string) {
	t.Helper()
	args := command.Args()
	for _, value := range values {
		found := false
		for _, arg := range args {
			if strings.EqualFold(stringifyRedisArg(arg), value) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("command %#v does not contain %q", args, value)
		}
	}
}
