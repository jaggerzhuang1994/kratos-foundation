package queue

import (
	"encoding/json"
	"testing"
)

func TestTaskClone(t *testing.T) {
	var empty *Task
	if empty.Clone() != nil {
		t.Fatal("nil clone")
	}
	original := &Task{ID: "one", Payload: []byte("data"), Headers: map[string]string{"a": "b"}}
	cloned := original.Clone()
	cloned.Payload[0] = 'X'
	cloned.Headers["a"] = "c"
	if string(original.Payload) != "data" || original.Headers["a"] != "b" {
		t.Fatal("shared snapshot")
	}
}

func TestTaskMessageVersionJSON(t *testing.T) {
	original := Task{ID: "one", MessageVersion: "EmailMessage.v1", Payload: []byte("x")}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["MessageVersion"]) != `"EmailMessage.v1"` {
		t.Fatalf("missing message version: %s", data)
	}
	if _, ok := fields["Type"]; ok {
		t.Fatalf("obsolete storage key: %s", data)
	}
	var decoded Task
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.MessageVersion != original.MessageVersion || string(decoded.Payload) != "x" {
		t.Fatalf("roundtrip=%+v", decoded)
	}
	if original.Clone().MessageVersion != original.MessageVersion {
		t.Fatal("clone lost version")
	}
}
