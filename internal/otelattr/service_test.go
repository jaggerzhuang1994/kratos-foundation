package otelattr

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
)

type testAppInfo struct{ id, name, version string }

var _ appinfo.AppInfo = testAppInfo{}

func (i testAppInfo) ID() string                  { return i.id }
func (i testAppInfo) Name() string                { return i.name }
func (i testAppInfo) Version() string             { return i.version }
func (i testAppInfo) Metadata() map[string]string { return nil }

func TestServiceAttributes(t *testing.T) {
	attrs := ServiceAttributes(testAppInfo{id: "instance-1", name: "orders", version: "v1"})
	got := map[string]string{}
	for _, attr := range attrs {
		got[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		string(semconv.ServiceNameKey):       "orders",
		string(semconv.ServiceInstanceIDKey): "instance-1",
		string(semconv.ServiceVersionKey):    "v1",
	} {
		if got[key] != want {
			t.Fatalf("attribute %q = %q, want %q", key, got[key], want)
		}
	}
}
