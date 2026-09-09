package database

import (
	"slices"
	"sync"
	"testing"
)

func stubDriver(DriverConfig) (DriverConnection, error) {
	return DriverConnection{}, nil
}

func TestDriverRegistryNormalizesAndFreezes(t *testing.T) {
	registry := newDriverRegistry()
	if err := registry.register(" MySQL ", stubDriver); err != nil {
		t.Fatal(err)
	}
	if got := registry.names(); !slices.Equal(got, []string{"mysql"}) {
		t.Fatalf("names = %v", got)
	}
	snapshot := registry.snapshotAndFreeze()
	if snapshot["mysql"] == nil {
		t.Fatal("snapshot omitted mysql")
	}
	if err := registry.register("sqlite3", stubDriver); err == nil {
		t.Fatal("register succeeded after freeze")
	}
}

func TestDriverRegistryRejectsInvalidRegistration(t *testing.T) {
	registry := newDriverRegistry()
	if err := registry.register("", stubDriver); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := registry.register("mysql", nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	if err := registry.register("mysql", stubDriver); err != nil {
		t.Fatal(err)
	}
	if err := registry.register("MYSQL", stubDriver); err == nil {
		t.Fatal("duplicate normalized name accepted")
	}
}

func TestDriverRegistryConcurrentNames(t *testing.T) {
	registry := newDriverRegistry()
	for _, name := range []string{"mysql", "sqlite3"} {
		if err := registry.register(name, stubDriver); err != nil {
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
