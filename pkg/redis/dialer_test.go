package redis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestManagedDialerUsesProgressiveBackoff(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "redis-dial-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	address := filepath.Join(directory, "absent.sock")
	synctest.Test(t, func(t *testing.T) {
		manager := newLocalTestManager(map[string]connectionOption{"cache": {
			Network: proto.String("unix"), Addr: proto.String(address),
			DialerRetries: proto.Int32(4), DialerRetryTimeout: durationpb.New(100 * time.Millisecond),
		}})
		client, err := manager.newConnection("cache")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		}()
		started := time.Now()
		_, err = client.Options().Dialer(context.Background(), "unix", address)
		if err == nil {
			t.Fatal("dial unexpectedly succeeded")
		}
		// Four attempts have three waits: 80–100, 160–200, 320–400 ms.
		if elapsed := time.Since(started); elapsed < 560*time.Millisecond || elapsed > 700*time.Millisecond {
			t.Fatalf("dial retry waits = %s, want 560–700ms", elapsed)
		}
		if client.Options().DialerRetries != 1 {
			t.Fatal("SDK must not repeat the managed dial loop")
		}
	})
}

func TestManagedDialerCancelsBackoff(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "redis-dial-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	address := filepath.Join(directory, "absent.sock")
	synctest.Test(t, func(t *testing.T) {
		manager := newLocalTestManager(map[string]connectionOption{"cache": &config_pb.RedisOption{
			Network: proto.String("unix"), Addr: proto.String(address), DialerRetries: proto.Int32(5),
		}})
		client, err := manager.newConnection("cache")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := client.Close(); err != nil {
				t.Error(err)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err = client.Options().Dialer(ctx, "unix", address)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 50*time.Millisecond {
			t.Fatalf("dial = %v after %s; want canceled backoff at 50ms", err, time.Since(started))
		}
	})
}
