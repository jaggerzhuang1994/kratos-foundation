package queue

import "testing"

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
