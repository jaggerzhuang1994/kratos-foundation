package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestManagedClientReconnectsThroughRealPoolAfterServerRestart(t *testing.T) {
	server := newRESPReconnectServer(t, "127.0.0.1:0")
	address := server.listener.Addr().String()
	manager := newLocalTestManager(map[string]connectionOption{"cache": {
		Addr: proto.String(address), Protocol: proto.Int32(2), DisableIdentity: proto.Bool(true),
		PoolSize: proto.Int32(1), MaxRetries: proto.Int32(-1), DialerRetries: proto.Int32(1),
		DialTimeout: durationpb.New(50 * time.Millisecond), DialerRetryTimeout: durationpb.New(time.Millisecond),
		ReadTimeout: durationpb.New(100 * time.Millisecond), WriteTimeout: durationpb.New(100 * time.Millisecond),
		ContextTimeoutEnabled: proto.Bool(true),
	}})
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	client, err := manager.Connection("cache")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if result, err := client.Ping(ctx).Result(); err != nil || result != "PONG" {
		t.Fatalf("initial PING = %q, %v", result, err)
	}

	// 断开已有连接，在容量为一的真实连接池中触发拨号失败与恢复探测。
	server.stop()
	for range 2 {
		if err := client.Ping(ctx).Err(); err == nil {
			t.Fatal("PING succeeded while server was down")
		}
	}
	if cached, err := manager.Connection("cache"); err != nil || cached != client {
		t.Fatalf("client changed during outage: %p != %p, %v", cached, client, err)
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := client.Ping(canceled).Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PING = %v", err)
	}

	restored := newRESPReconnectServer(t, address)
	// 保持原 Client，通过带截止时间的轮询观察 SDK 后台探测恢复。
	for {
		if result, err := client.Ping(ctx).Result(); err == nil && result == "PONG" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("original client did not recover: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if cached, err := manager.Connection("cache"); err != nil || cached != client {
		t.Fatalf("client changed after recovery: %p != %p, %v", cached, client, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx).Err(); !errors.Is(err, goredis.ErrClosed) {
		t.Fatalf("PING after manager cleanup = %v", err)
	}
	if _, err := manager.Connection("cache"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Connection after manager cleanup = %v", err)
	}
	restored.stop()
}

// respReconnectServer 仅实现 RESP2 命令帧、HELLO 回退与 PING，
// 足以覆盖真实 go-redis 初始化、连接断开和连接池恢复路径。
type respReconnectServer struct {
	listener net.Listener
	accepted chan struct{}
	mu       sync.Mutex
	clients  map[net.Conn]struct{}
	workers  sync.WaitGroup
	stopOnce sync.Once
}

func newRESPReconnectServer(t *testing.T, address string) *respReconnectServer {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &respReconnectServer{listener: listener, accepted: make(chan struct{}), clients: make(map[net.Conn]struct{})}
	t.Cleanup(server.stop)
	go func() {
		defer close(server.accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.clients[conn] = struct{}{}
			server.mu.Unlock()
			server.workers.Add(1)
			go server.serve(conn)
		}
	}()
	return server
}

func (s *respReconnectServer) serve(conn net.Conn) {
	defer s.workers.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()
	reader := bufio.NewReader(conn)
	for {
		command, err := readRESPReconnectCommand(reader)
		if err != nil {
			return
		}
		response := "-ERR unknown command\r\n"
		if command == "ping" {
			response = "+PONG\r\n"
		}
		if _, err := io.WriteString(conn, response); err != nil {
			return
		}
	}
}

func (s *respReconnectServer) stop() {
	s.stopOnce.Do(func() {
		_ = s.listener.Close()
		<-s.accepted
		s.mu.Lock()
		for conn := range s.clients {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.workers.Wait()
	})
}

func readRESPReconnectCommand(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "*") {
		return "", fmt.Errorf("expected RESP array, got %q", line)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return "", err
	}
	var command string
	for index := 0; index < count; index++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(line, "$") {
			return "", fmt.Errorf("expected RESP string, got %q", line)
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 {
			return "", fmt.Errorf("invalid RESP length %q", line)
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		if index == 0 {
			command = strings.ToLower(string(value[:length]))
		}
	}
	return command, nil
}
