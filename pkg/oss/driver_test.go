package oss

import (
	"bytes"
	"slices"
	"strings"
	"sync"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

func stubOSSDriver(BucketConfig) (Bucket, error) {
	return nil, nil
}

func TestDriverRegistryNormalizesAndFreezes(t *testing.T) {
	registry := newDriverRegistry()
	if err := registry.register(" Aliyun ", stubOSSDriver); err != nil {
		t.Fatal(err)
	}
	if got := registry.names(); !slices.Equal(got, []string{"aliyun"}) {
		t.Fatalf("names = %v", got)
	}
	snapshot := registry.snapshotAndFreeze()
	if snapshot["aliyun"] == nil {
		t.Fatal("snapshot omitted aliyun")
	}
	if err := registry.register("qiniu", stubOSSDriver); err == nil {
		t.Fatal("register succeeded after freeze")
	}
}

func TestDriverRegistryRejectsInvalidRegistration(t *testing.T) {
	registry := newDriverRegistry()
	if err := registry.register("", stubOSSDriver); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := registry.register("aliyun", nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	if err := registry.register("aliyun", stubOSSDriver); err != nil {
		t.Fatal(err)
	}
	if err := registry.register("ALIYUN", stubOSSDriver); err == nil {
		t.Fatal("duplicate normalized name accepted")
	}
}

func TestDriverRegistryConcurrentNames(t *testing.T) {
	registry := newDriverRegistry()
	for _, name := range []string{"aliyun", "qiniu"} {
		if err := registry.register(name, stubOSSDriver); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = registry.names()
		}()
	}
	wait.Wait()
}

func TestPublicDriverRegistryFunctions(t *testing.T) {
	previous := ossDrivers
	previousLogger := foundationlog.GetLogger()
	ossDrivers = newDriverRegistry()
	t.Cleanup(func() {
		ossDrivers = previous
		foundationlog.SetLogger(previousLogger)
	})
	var buffer bytes.Buffer
	foundationlog.SetLogger(kratoslog.NewStdLogger(&buffer))

	if err := RegisterDriver(" Zeta ", stubOSSDriver); err != nil {
		t.Fatal(err)
	}
	MustRegisterDriver("alpha", stubOSSDriver)
	for _, want := range []string{"INFO", "module=oss", "driver=zeta", "driver=alpha"} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("missing %q in %s", want, buffer.String())
		}
	}
	if got := strings.Count(buffer.String(), "Registered OSS driver"); got != 2 {
		t.Fatalf("successful registration log count = %d, want 2", got)
	}
	if got := RegisteredDrivers(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("RegisteredDrivers() = %v", got)
	}
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("MustRegisterDriver did not panic for a duplicate")
		}
		if buffer.Len() != 0 {
			t.Fatalf("failed registration logged success: %s", buffer.String())
		}
	}()
	buffer.Reset()
	MustRegisterDriver("ALPHA", stubOSSDriver)
}
