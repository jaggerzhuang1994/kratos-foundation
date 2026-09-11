package kafka

import (
	"slices"
	"testing"
)

func TestHeaderCarrierKeysReturnsUniqueKeysInFirstSeenOrder(t *testing.T) {
	carrier := headerCarrier{message: &Message{Headers: []Header{
		{Key: "traceparent", Value: []byte("first")},
		{Key: "baggage", Value: []byte("one")},
		{Key: "traceparent", Value: []byte("second")},
		{Key: "x-request-id", Value: []byte("request-1")},
		{Key: "baggage", Value: []byte("two")},
	}}}

	want := []string{"traceparent", "baggage", "x-request-id"}
	if got := carrier.Keys(); !slices.Equal(got, want) {
		t.Fatalf("headerCarrier.Keys() = %#v, want %#v", got, want)
	}
}

func TestHeaderCarrierKeysReturnsEmptyNonNilSliceWithoutHeaders(t *testing.T) {
	got := (headerCarrier{message: &Message{}}).Keys()
	if got == nil || len(got) != 0 {
		t.Fatalf("headerCarrier.Keys() = %#v, want empty non-nil slice", got)
	}
}
