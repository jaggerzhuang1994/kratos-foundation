package queue

import (
	"errors"
	"testing"
)

func TestDefinitionValidationAndJSON(t *testing.T) {
	for _, d := range []Definition[string]{{}, {Queue: "q", MessageType: "m"}, {Queue: " q", MessageType: "m", Version: 1}, {Queue: "q", MessageType: "m ", Version: 1}} {
		if _, err := d.resolve(); err == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
	d, err := (Definition[string]{Queue: "q", MessageType: "m", Version: 2}).resolve()
	if err != nil || d.taskType() != "m.v2" {
		t.Fatalf("definition: %+v %v", d, err)
	}
	data, err := d.Codec.Encode("hello")
	if err != nil {
		t.Fatal(err)
	}
	message, err := d.Codec.Decode(data)
	if err != nil || message != "hello" {
		t.Fatalf("roundtrip: %q %v", message, err)
	}
	if _, err := (JSONCodec[chan int]{}).Encode(make(chan int)); err == nil {
		t.Fatal("encoded unsupported JSON value")
	}
	if _, err := d.Codec.Decode([]byte("{")); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}

type stringCodec struct{ err error }

func (c stringCodec) Encode(message string) ([]byte, error) { return []byte(message), c.err }
func (c stringCodec) Decode(data []byte) (string, error)    { return string(data), c.err }

func TestDefinitionCustomCodec(t *testing.T) {
	d, err := (Definition[string]{Queue: "q", MessageType: "m", Version: 1, Codec: stringCodec{}}).resolve()
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.Codec.Encode("raw")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := d.Codec.Decode(data); err != nil || value != "raw" {
		t.Fatalf("custom codec: %q %v", value, err)
	}
	want := errors.New("codec failed")
	if _, err := (stringCodec{err: want}).Decode(nil); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
