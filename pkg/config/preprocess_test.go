package config

import (
	"context"
	"errors"
	"testing"
)

func TestPreprocessPreservesRawTemplateAndExpandsOnce(t *testing.T) {
	t.Setenv("FOUNDATION_TEMPLATE_PORT", "8080")
	raw := newTestSource(`{"port":${FOUNDATION_TEMPLATE_PORT},"literal":"$${UNCHANGED}"}`)
	wrapper := &preprocessedSource{source: raw, expand: true}
	values, err := wrapper.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(values[0].Value) != `{"port":8080,"literal":"${UNCHANGED}"}` {
		t.Fatal(string(values[0].Value))
	}
	t.Setenv("FOUNDATION_TEMPLATE_PORT", "9090")
	values, err = wrapper.Load()
	if err != nil || string(values[0].Value) != `{"port":9090,"literal":"${UNCHANGED}"}` {
		t.Fatal(values, err)
	}
	watcher, err := wrapper.Watch()
	if err != nil {
		t.Fatal(err)
	}
	raw.watcher.events <- jsonValues(`{"port":${FOUNDATION_TEMPLATE_PORT}}`)
	values, err = watcher.Next()
	if err != nil || string(values[0].Value) != `{"port":9090}` {
		t.Fatal(values, err)
	}
	if err := watcher.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := watcher.Next(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPreprocessErrorsAndCopies(t *testing.T) {
	t.Setenv("FOUNDATION_TEMPLATE_REQUIRED", "")
	for _, content := range []string{"${", "${FOUNDATION_TEMPLATE_REQUIRED:?required}"} {
		if _, err := expandEnvironment(content); err == nil {
			t.Fatalf("invalid template accepted: %q", content)
		}
	}
	wrapper := &preprocessedSource{expand: true}
	if _, err := wrapper.preprocess([]*KeyValue{{Key: "${", Value: []byte("x")}}); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err := wrapper.preprocess([]*KeyValue{{Key: "test", Value: []byte("${")}}); err == nil {
		t.Fatal("invalid value accepted")
	}
	wrapper.expand = false
	original := []*KeyValue{nil, {Key: "test", Value: []byte("$raw")}}
	values, err := wrapper.preprocess(original)
	if err != nil {
		t.Fatal(err)
	}
	values[0].Value[0] = 'x'
	if string(original[1].Value) != "$raw" {
		t.Fatal("source buffer mutated")
	}
}
