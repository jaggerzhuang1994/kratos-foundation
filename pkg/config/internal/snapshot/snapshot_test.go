package snapshot

import "testing"

func TestSnapshotLooksUpNestedPaths(t *testing.T) {
	t.Parallel()

	current := &Snapshot{values: map[string]any{
		"service": map[string]any{"port": 8080},
	}}
	if value, found := current.Lookup("service.port"); !found || value != 8080 {
		t.Fatalf("Lookup service.port = (%#v, %t), want 8080", value, found)
	}
	if _, found := current.Lookup("service.port.value"); found {
		t.Fatal("Lookup traversed through a scalar")
	}
	var empty *Snapshot
	if _, found := empty.Lookup("service"); found {
		t.Fatal("nil Snapshot returned a value")
	}
}
