package sqlite

import (
	"slices"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
)

func TestDriverRegistered(t *testing.T) {
	if !slices.Contains(database.RegisteredDrivers(), DriverName) {
		t.Fatalf("registered drivers = %v", database.RegisteredDrivers())
	}
}

func TestNewConnection(t *testing.T) {
	connection, err := newConnection(database.DriverConfig{
		Name: "local",
		DSN:  ":memory:",
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.SQLDB == nil || connection.Dialector == nil {
		t.Fatalf("connection = %#v", connection)
	}
	if err := connection.SQLDB.Close(); err != nil {
		t.Fatal(err)
	}
}
