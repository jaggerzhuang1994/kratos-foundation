package dbroute

import (
	"context"
	"testing"
)

func TestPrimaryMarker(t *testing.T) {
	if Primary(context.Background()) || !Primary(WithPrimary(context.Background())) {
		t.Fatal("primary marker not carried by context")
	}
}
