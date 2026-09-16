package client

import (
	"math"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNewClientSpecUsesDiscoveryDefault(t *testing.T) {
	t.Parallel()
	spec := newClientSpec("orders", nil, nil)
	if spec.protocol != config_pb.Protocol_GRPC || spec.target != "discovery:///orders" {
		t.Fatalf("default spec = %s %q", spec.protocol, spec.target)
	}
}

func TestEffectiveDefaultMatchesEmptyOption(t *testing.T) {
	t.Parallel()
	if !newClientSpec("orders", nil, nil).equal(newClientSpec("orders", new(config_pb.ClientOption), nil)) {
		t.Fatal("absent and empty options differ")
	}
}

func TestClientSpecCanonicalizesMiddlewareDefaults(t *testing.T) {
	t.Parallel()
	falseValue := false
	trueValue := true
	zeroSuccess := 0.0
	nonzeroDeadline := durationpb.New(time.Second)
	routeWithoutDuration := &config_pb.Middleware_Deadline_RouteRule{
		Rule: &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/orders.Get"},
	}
	routeWithZeroDuration := &config_pb.Middleware_Deadline_RouteRule{
		Rule:            &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/orders.Get"},
		FallbackTimeout: durationpb.New(0),
	}

	equivalent := []struct {
		name  string
		left  *config_pb.ClientMiddleware
		right *config_pb.ClientMiddleware
	}{
		{name: "empty logging", left: &config_pb.ClientMiddleware{Logging: new(config_pb.Middleware_Logging)}},
		{name: "empty deadline", left: &config_pb.ClientMiddleware{Deadline: new(config_pb.Middleware_Deadline)}},
		{name: "empty metadata", left: &config_pb.ClientMiddleware{Metadata: new(config_pb.Middleware_Metadata)}},
		{name: "empty tracing", left: &config_pb.ClientMiddleware{Tracing: new(config_pb.Middleware_Tracing)}},
		{name: "empty metrics", left: &config_pb.ClientMiddleware{Metrics: new(config_pb.Middleware_Metrics)}},
		{name: "explicit false metadata disable", left: &config_pb.ClientMiddleware{Metadata: &config_pb.Middleware_Metadata{Disable: &falseValue}}},
		{name: "explicit false tracing disable", left: &config_pb.ClientMiddleware{Tracing: &config_pb.Middleware_Tracing{Disable: &falseValue}}},
		{name: "explicit false metrics disable", left: &config_pb.ClientMiddleware{Metrics: &config_pb.Middleware_Metrics{Disable: &falseValue}}},
		{name: "explicit false logging disable", left: &config_pb.ClientMiddleware{Logging: &config_pb.Middleware_Logging{Disable: &falseValue}}},
		{name: "zero max deadline", left: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(0)}}},
		{name: "zero minimum budget", left: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{MinBudget: durationpb.New(0)}}},
		{
			name: "circuit breaker without enable",
			left: &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{
				Sre: &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: &zeroSuccess},
			}},
		},
		{
			name: "explicit false circuit breaker enable",
			left: &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{
				Enable: &falseValue,
				Sre:    &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: &zeroSuccess},
			}},
		},
		{
			name:  "enabled circuit breaker with empty SRE",
			left:  &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{Enable: &trueValue, Sre: new(config_pb.Middleware_CircuitBreaker_SREBreaker)}},
			right: &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{Enable: &trueValue}},
		},
	}
	for _, tt := range equivalent {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			left := newClientSpec("orders", &config_pb.ClientOption{Middleware: tt.left}, nil)
			right := newClientSpec("orders", &config_pb.ClientOption{Middleware: tt.right}, nil)
			if !left.equal(right) {
				t.Fatal("default-equivalent middleware produced different client specs")
			}
		})
	}

	different := []struct {
		name  string
		left  *config_pb.ClientMiddleware
		right *config_pb.ClientMiddleware
	}{
		{
			name: "zero fallback disables default timeout",
			left: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)}},
		},
		{
			name: "disabled logging",
			left: &config_pb.ClientMiddleware{Logging: &config_pb.Middleware_Logging{Disable: &trueValue}},
		},
		{
			name: "nonzero top-level deadline",
			left: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{FallbackTimeout: nonzeroDeadline}},
		},
		{
			name:  "route zero duration overrides inheritance",
			left:  &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{Routes: []*config_pb.Middleware_Deadline_RouteRule{routeWithZeroDuration}}},
			right: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{Routes: []*config_pb.Middleware_Deadline_RouteRule{routeWithoutDuration}}},
		},
		{
			name:  "SRE scalar presence selects explicit value",
			left:  &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{Enable: &trueValue, Sre: &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: &zeroSuccess}}},
			right: &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{Enable: &trueValue}},
		},
	}
	for _, tt := range different {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			left := newClientSpec("orders", &config_pb.ClientOption{Middleware: tt.left}, nil)
			right := newClientSpec("orders", &config_pb.ClientOption{Middleware: tt.right}, nil)
			if left.equal(right) {
				t.Fatal("effective middleware change produced equal client specs")
			}
		})
	}
}

func TestClientRejectsInvalidSREConfiguration(t *testing.T) {
	builder := newTestRealBuilder(t, nil)
	for _, tt := range []struct {
		name    string
		breaker *config_pb.Middleware_CircuitBreaker_SREBreaker
	}{
		{"zero bucket", &config_pb.Middleware_CircuitBreaker_SREBreaker{Bucket: proto.Int32(0)}},
		{"negative bucket", &config_pb.Middleware_CircuitBreaker_SREBreaker{Bucket: proto.Int32(-1)}},
		{"zero window", &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(0)}},
		{"negative window", &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(-time.Second)}},
		{"invalid duration", &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: &durationpb.Duration{Nanos: 1_000_000_000}}},
		{"overflow duration", &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: &durationpb.Duration{Seconds: 10_000_000_000}}},
		{"zero bucket duration", &config_pb.Middleware_CircuitBreaker_SREBreaker{Window: durationpb.New(time.Nanosecond)}},
		{"negative request", &config_pb.Middleware_CircuitBreaker_SREBreaker{Request: proto.Int64(-1)}},
		{"zero success", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(0)}},
		{"negative success", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(-0.1)}},
		{"success greater than one", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(1.1)}},
		{"nan success", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(math.NaN())}},
		{"infinite success", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(math.Inf(1))}},
		{"overflow reciprocal", &config_pb.Middleware_CircuitBreaker_SREBreaker{Success: proto.Float64(math.SmallestNonzeroFloat64)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := configWithTarget("orders", "127.0.0.1:1")
			config.Clients["orders"].Middleware = &config_pb.ClientMiddleware{CircuitBreaker: &config_pb.Middleware_CircuitBreaker{Enable: proto.Bool(true), Sre: tt.breaker}}
			if err := builder.validateConfig(config); err == nil {
				t.Fatal("enabled invalid SRE configuration was accepted")
			}
			config.Clients["orders"].Middleware.CircuitBreaker.Enable = proto.Bool(false)
			if err := builder.validateConfig(config); err != nil {
				t.Fatalf("disabled SRE configuration must be ignored: %v", err)
			}
		})
	}
}

func TestClientSpecInheritsRootDefaults(t *testing.T) {
	root := &config_pb.Client{FallbackTimeout: durationpb.New(5 * time.Second), MaxTimeout: durationpb.New(8 * time.Second), MinBudget: durationpb.New(time.Second), Discovery: proto.String("regional")}
	for _, tt := range []struct {
		name      string
		option    *config_pb.ClientOption
		want      *config_pb.Middleware_Deadline
		discovery string
	}{
		{"absent client", nil, &config_pb.Middleware_Deadline{FallbackTimeout: root.FallbackTimeout, MaxTimeout: root.MaxTimeout, MinBudget: root.MinBudget}, "regional"},
		{"partial override", &config_pb.ClientOption{Middleware: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(3 * time.Second)}}}, &config_pb.Middleware_Deadline{FallbackTimeout: root.FallbackTimeout, MaxTimeout: durationpb.New(3 * time.Second), MinBudget: root.MinBudget}, "regional"},
		{"explicit zero", &config_pb.ClientOption{Discovery: "custom", Middleware: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0), MaxTimeout: durationpb.New(0), MinBudget: durationpb.New(0)}}}, &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)}, "custom"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beforeRoot, beforeOption := proto.CloneOf(root), proto.CloneOf(tt.option)
			spec := newClientSpec("orders", tt.option, root)
			if spec.discovery != tt.discovery || !proto.Equal(spec.middleware.Deadline, tt.want) {
				t.Fatalf("effective spec = %s %v", spec.discovery, spec.middleware.Deadline)
			}
			if !proto.Equal(root, beforeRoot) || !proto.Equal(tt.option, beforeOption) {
				t.Fatal("input configuration mutated")
			}
			spec.middleware.Deadline.FallbackTimeout.Seconds = 99
			if !proto.Equal(root, beforeRoot) || !proto.Equal(tt.option, beforeOption) {
				t.Fatal("effective configuration aliases input")
			}
		})
	}
}

func TestClientRejectsInvalidRootDefaults(t *testing.T) {
	b := newTestRealBuilder(t, nil)
	for _, tt := range []struct {
		name   string
		config *config_pb.Client
	}{
		{"negative fallback", &config_pb.Client{FallbackTimeout: durationpb.New(-time.Second)}},
		{"negative max", &config_pb.Client{MaxTimeout: durationpb.New(-time.Second)}},
		{"negative budget", &config_pb.Client{MinBudget: durationpb.New(-time.Second)}},
		{"invalid duration", &config_pb.Client{MaxTimeout: &durationpb.Duration{Nanos: 1000000000}}},
		{"root conflict", &config_pb.Client{MaxTimeout: durationpb.New(time.Second), MinBudget: durationpb.New(2 * time.Second)}},
		{"inherited conflict", &config_pb.Client{MinBudget: durationpb.New(2 * time.Second), Clients: map[string]*config_pb.ClientOption{"orders": {Target: "localhost:9000", Middleware: &config_pb.ClientMiddleware{Deadline: &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(time.Second)}}}}}},
		{"missing discovery", &config_pb.Client{Discovery: proto.String("missing"), Clients: map[string]*config_pb.ClientOption{"orders": nil}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := b.validateConfig(tt.config); err == nil {
				t.Fatal("invalid defaults accepted")
			}
		})
	}
}
