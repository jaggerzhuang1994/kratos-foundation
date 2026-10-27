package observability

import "testing"

func TestNewKeepsProviders(t *testing.T) {
	if got := New(nil, nil, nil); got != (Observability{}) {
		t.Fatalf("New(nil...) = %+v", got)
	}
}
