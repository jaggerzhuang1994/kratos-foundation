package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebsocketHubServeAndStopAreConcurrencySafe(t *testing.T) {
	hub := newWebSocketHub()
	client := &websocketClient{}
	if !hub.serve(client) {
		t.Fatal("hub rejected client before stopping")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := hub.stop(ctx); err != nil && !strings.Contains(err.Error(), "websocket connection is nil") {
				t.Errorf("stop: %v", err)
			}
		})
	}
	wg.Wait()
	if hub.serve(&websocketClient{}) {
		t.Fatal("hub accepted client after stopping")
	}
}
