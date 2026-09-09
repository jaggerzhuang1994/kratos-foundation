package deadline

import (
	"context"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestStoreFallbackDefaultAndOverrides(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   time.Duration
		source Source
	}{
		{name: "missing deadline", want: 10 * time.Second, source: SourceFallback},
		{name: "empty deadline", config: &config_pb.Middleware_Deadline{}, want: 10 * time.Second, source: SourceFallback},
		{
			name:   "explicit fallback",
			config: &config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(3 * time.Second)},
			want:   3 * time.Second, source: SourceFallback,
		},
		{
			name:   "max caps default",
			config: &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(2 * time.Second)},
			want:   2 * time.Second, source: SourceMax,
		},
		{
			name:   "max does not extend default",
			config: &config_pb.Middleware_Deadline{MaxTimeout: durationpb.New(20 * time.Second)},
			want:   10 * time.Second, source: SourceFallback,
		},
		{
			name: "zero fallback keeps max",
			config: &config_pb.Middleware_Deadline{
				FallbackTimeout: durationpb.New(0), MaxTimeout: durationpb.New(2 * time.Second),
			},
			want: 2 * time.Second, source: SourceMax,
		},
		{
			name: "route inherits default",
			config: &config_pb.Middleware_Deadline{Routes: []*config_pb.Middleware_Deadline_RouteRule{
				{Rule: &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/call"}},
			}},
			want: 10 * time.Second, source: SourceFallback,
		},
		{
			name: "route disables default",
			config: &config_pb.Middleware_Deadline{Routes: []*config_pb.Middleware_Deadline_RouteRule{
				{
					Rule:            &config_pb.Middleware_Deadline_RouteRule_Path{Path: "/call"},
					FallbackTimeout: durationpb.New(0),
				},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(tt.config)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel, err := store.Derive(context.Background(), "/call")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			deadline, hasDeadline := ctx.Deadline()
			info, hasInfo := InfoFromContext(ctx)
			if tt.want == 0 {
				if hasDeadline || hasInfo {
					t.Fatal("disabled fallback created a deadline")
				}
				return
			}
			if !hasDeadline || !hasInfo || !deadline.Equal(info.EffectiveDeadline) || info.RemainingAtApply != tt.want || info.Source != tt.source {
				t.Fatalf("deadline=%s info=%+v, want %s from %s", deadline, info, tt.want, tt.source)
			}
		})
	}
}

func TestStoreDefaultFallbackDoesNotShortenParentDeadline(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithTimeout(context.Background(), time.Minute)
	defer cancelParent()
	ctx, cancel, err := store.Derive(parent, "/call")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	want, _ := parent.Deadline()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("deadline=%s, want parent deadline %s", got, want)
	}
}

func TestStoreUpdateMissingFallbackRestoresDefault(t *testing.T) {
	store, err := NewStore(&config_pb.Middleware_Deadline{FallbackTimeout: durationpb.New(0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(nil); err != nil {
		t.Fatal(err)
	}
	info := deriveInfo(t, store, "/call")
	if info.RemainingAtApply != 10*time.Second || info.Source != SourceFallback {
		t.Fatalf("restored deadline info = %+v", info)
	}
}
