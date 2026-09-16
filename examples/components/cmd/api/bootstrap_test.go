package main

import (
	"context"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/redis/go-redis/v9"
)

func TestInitializeAndCleanup(t *testing.T) {
	t.Setenv("DISABLE_CONSUL", "true")
	t.Setenv("LOG_FILE_ENABLE", "false")
	path, err := filepath.Abs("../../configs/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "config.yaml")
	content = []byte(strings.ReplaceAll(string(content), "/tmp/components.db", filepath.Join(t.TempDir(), "test.db")))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	application, cleanup, err := initialize(configPath(path), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if application == nil {
		t.Fatal("application is nil")
	}
}

func TestSourcesRejectsDirectory(t *testing.T) {
	t.Setenv("LOG_FILE_ENABLE", "false")
	if _, err := newSpec(app.NewSpec(), server.NewSpec(), job.NewSpec(), configPath(t.TempDir())); err == nil {
		t.Fatal("directory accepted as configuration")
	}
}

func TestRedisJobs(t *testing.T) {
	t.Setenv("LOG_FILE_ENABLE", "false")
	logger, closeLog, err := log.NewLogger()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLog)
	cache := redis.NewClient(&redis.Options{Addr: "unused"})
	cache.AddHook(&demoRedisReadHook{})
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"redis-heartbeat", "cache-size", "cache-ttl"} {
		t.Run(name, func(t *testing.T) {
			if err := runRedisJob(context.Background(), orderRedis{cache}, name, logger); err != nil {
				t.Fatal(err)
			}
		})
	}
}
