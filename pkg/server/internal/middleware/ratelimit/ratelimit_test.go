package ratelimit

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestServerDisablesNilAndDisabledConfigurations(t *testing.T) {
	if got := Server(nil); got != nil {
		t.Fatalf("Server(nil) = %v, want nil", got)
	}
	if got := Server(&config_pb.Middleware_RateLimit{}); got != nil {
		t.Fatalf("Server(disabled) = %v, want nil", got)
	}
}

func TestServerEnabledWrapsHandlerAndAllowsRequest(t *testing.T) {
	enabled := true
	mw := Server(&config_pb.Middleware_RateLimit{Enable: &enabled})
	if mw == nil {
		t.Fatal("Server(enabled) = nil")
	}
	called := false
	response, err := mw(func(context.Context, any) (any, error) {
		called = true
		return "ok", nil
	})(context.Background(), "request")
	if err != nil || !called || response != "ok" {
		t.Fatalf("wrapped handler = (%v, %v), called=%v", response, err, called)
	}
}

func TestServerEnabledPropagatesHandlerError(t *testing.T) {
	enabled := true
	want := errors.New("handler failed")
	_, err := Server(&config_pb.Middleware_RateLimit{Enable: &enabled})(func(context.Context, any) (any, error) { return nil, want })(context.Background(), nil)
	if !errors.Is(err, want) {
		t.Fatalf("middleware error = %v, want %v", err, want)
	}
}

func TestBBRLimiterAcceptsNilAndConfiguredOptions(t *testing.T) {
	if newBBRLimiter(nil) == nil {
		t.Fatal("nil config produced nil limiter")
	}
	window := durationpb.New(time.Second)
	bucket := int32(10)
	threshold, quota := int64(800), float64(2)
	if newBBRLimiter(&config_pb.Middleware_RateLimit_BBRLimiter{Window: window, Bucket: &bucket, CpuThreshold: &threshold, CpuQuota: &quota}) == nil {
		t.Fatal("configured BBR limiter is nil")
	}
}

func TestValidateBBRDefaultsAndBucketDurationBoundaries(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		config  *config_pb.Middleware_RateLimit_BBRLimiter
		wantErr bool
	}{
		{name: "defaults"},
		{name: "one nanosecond bucket", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(100 * time.Nanosecond), CpuQuota: proto.Float64(0)}},
		{name: "one second bucket", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(100 * time.Second)}},
		{name: "below one nanosecond", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(99 * time.Nanosecond)}, wantErr: true},
		{name: "above one second", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(100*time.Second + 100*time.Nanosecond)}, wantErr: true},
		{name: "large bucket count small window", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(time.Second), Bucket: proto.Int32(math.MaxInt32)}, wantErr: true},
		{name: "maximum duration", config: &config_pb.Middleware_RateLimit_BBRLimiter{Window: durationpb.New(math.MaxInt64)}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_pb.Middleware_RateLimit{Enable: proto.Bool(true), BbrLimiter: tt.config}
			if err := Validate(config); (err != nil) != tt.wantErr {
				t.Fatalf("Validate = %v, wantErr=%t", err, tt.wantErr)
			}
		})
	}
}
