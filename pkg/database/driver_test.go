package database

import (
	"bytes"
	"slices"
	"strings"
	"sync"
	"testing"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
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

func TestRegisterDriverLogsSuccessfulRegistration(t *testing.T) {
	previous, previousRegistry := kratoslog.GetLogger(), databaseDrivers
	t.Cleanup(func() { kratoslog.SetLogger(previous); databaseDrivers = previousRegistry })
	databaseDrivers = newDriverRegistry()
	var buffer bytes.Buffer
	foundationlog.SetLogger(kratoslog.NewStdLogger(&buffer))
	if err := RegisterDriver(" SQLite3 ", stubDriver); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"module=database", "function=RegisterDriver", "driver=sqlite3", "Registered database driver"} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("missing %q in %s", want, buffer.String())
		}
	}
	buffer.Reset()
	if err := RegisterDriver("sqlite3", stubDriver); err == nil {
		t.Fatal("duplicate accepted")
	}
	if buffer.Len() != 0 {
		t.Fatal("failed registration logged success")
	}
}
