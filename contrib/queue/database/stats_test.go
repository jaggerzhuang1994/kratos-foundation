package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
)

type statsRepo struct {
	Repo
	result queue.Stats
	err    error
}

func (r statsRepo) Stats(context.Context, time.Time) (queue.Stats, error) { return r.result, r.err }

func TestStats(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	want := queue.Stats{Ready: 4, OldestReadyKnown: true, OldestReadyAt: now}
	got, err := NewStore(statsRepo{result: want}).Stats(ctx, now)
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	sentinel := errors.New("offline")
	if _, err := NewStore(statsRepo{err: sentinel}).Stats(ctx, now); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := NewStore(repoStub{}).Stats(ctx, now); err == nil {
		t.Fatal("unsupported repo accepted")
	}
}
