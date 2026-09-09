package oss

import (
	"slices"
	"sync"
	"testing"
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
	ossDrivers = newDriverRegistry()
	t.Cleanup(func() { ossDrivers = previous })

	if err := RegisterDriver(" Zeta ", stubOSSDriver); err != nil {
		t.Fatal(err)
	}
	MustRegisterDriver("alpha", stubOSSDriver)
	if got := RegisteredDrivers(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("RegisteredDrivers() = %v", got)
	}
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("MustRegisterDriver did not panic for a duplicate")
		}
	}()
	MustRegisterDriver("ALPHA", stubOSSDriver)
}
