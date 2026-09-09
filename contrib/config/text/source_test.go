package text

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

// These cases kill mutations that drop constructor validation, reuse Load's
// mutable byte slice, or share the stop channel between watchers.
func TestNewSourceValidatesIdentity(t *testing.T) {
	for _, test := range []struct {
		name, key, format string
		wantErr           bool
	}{
		{name: "valid", key: "app", format: config.JSONFormat},
		{name: "empty key", format: config.JSONFormat, wantErr: true},
		{name: "key whitespace", key: " app", format: config.JSONFormat, wantErr: true},
		{name: "empty format", key: "app", wantErr: true},
		{name: "format whitespace", key: "app", format: " json", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewSource(test.key, test.format, "content")
			if test.wantErr {
				if err == nil {
					t.Fatal("NewSource error = nil")
				}
				return
			}
			if err != nil || source == nil {
				t.Fatalf("NewSource() = %v, %v", source, err)
			}
		})
	}
}

func TestSourceLoadReturnsIndependentValues(t *testing.T) {
	s, err := NewSource("app", config.JSONFormat, "original")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Key != "app" || first[0].Format != config.JSONFormat || string(first[0].Value) != "original" {
		t.Fatalf("Load() = %#v", first)
	}
	first[0].Value[0] = 'X'
	second, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(second[0].Value); got != "original" {
		t.Fatalf("second Load value = %q, want original", got)
	}
}

func TestWatcherStopIsIdempotentAndIndependent(t *testing.T) {
	s, err := NewSource("app", config.JSONFormat, "{}")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Watch()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	values, err := first.Next()
	if !errors.Is(err, context.Canceled) || values != nil {
		t.Fatalf("stopped watcher Next() = %v, %v", values, err)
	}

	secondIdle, ok := second.(*idleWatcher)
	if !ok {
		t.Fatalf("Watch() returned %T, want *idleWatcher", second)
	}
	select {
	case <-secondIdle.done:
		t.Fatal("stopping the first watcher also stopped the second")
	default:
	}
	done := make(chan error, 1)
	go func() { _, err := second.Next(); done <- err }()
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second Next error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second watcher to stop")
	}
}
