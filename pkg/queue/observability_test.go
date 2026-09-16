package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	dto "github.com/prometheus/client_model/go"
)

type statsSource func(context.Context, time.Time) (Stats, error)

func (s statsSource) Stats(ctx context.Context, now time.Time) (Stats, error) { return s(ctx, now) }

func TestRegisterStats(t *testing.T) {
	provider, closeProvider, err := metrics.NewProvider(appinfo.New("queue-stats-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	mode := "ready"
	source := statsSource(func(ctx context.Context, now time.Time) (Stats, error) {
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(now.Add(time.Second)) {
			t.Error("missing bounded deadline")
		}
		switch mode {
		case "error":
			return Stats{}, errors.New("offline")
		case "unknown":
			return Stats{Ready: 1001}, nil
		case "empty":
			return Stats{OldestReadyKnown: true}, nil
		default:
			return Stats{Ready: 2, Scheduled: 3, Running: 4, Failed: 5, OldestReadyKnown: true, OldestReadyAt: now.Add(-time.Minute)}, nil
		}
	})
	cleanup, err := RegisterStats("email", source, provider, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gather := func() map[string]*dto.MetricFamily {
		t.Helper()
		families, err := provider.PrometheusGatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]*dto.MetricFamily{}
		for _, family := range families {
			result[family.GetName()] = family
		}
		return result
	}
	for _, tc := range []struct {
		name           string
		success, known float64
		age            bool
	}{
		{"ready", 1, 1, true}, {"unknown", 1, 0, false}, {"error", 0, 0, false}, {"empty", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode = tc.name
			got := gather()
			for name, want := range map[string]float64{"queue_stats_collection_success": tc.success, "queue_stats_oldest_ready_known": tc.known} {
				family := got[name]
				if family == nil || len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != want {
					t.Fatalf("%s: %v", name, family)
				}
			}
			age := got["queue_oldest_ready_age_seconds"]
			if (age != nil) != tc.age {
				t.Fatalf("age presence: %v", age)
			}
			if tc.age {
				want := float64(0)
				if tc.name == "ready" {
					want = 60
				}
				if age.Metric[0].GetGauge().GetValue() != want {
					t.Fatalf("age: %v", age)
				}
			}
			if tc.name == "error" && got["queue_tasks"] != nil {
				t.Fatal("failed collection reported task counts")
			}
			if tc.name == "ready" {
				tasks := got["queue_tasks"]
				if tasks == nil || len(tasks.Metric) != 4 {
					t.Fatal("missing task states")
				}
				expected := map[string]float64{"ready": 2, "scheduled": 3, "running": 4, "failed": 5}
				for _, sample := range tasks.Metric {
					labels := map[string]string{}
					for _, label := range sample.Label {
						labels[label.GetName()] = label.GetValue()
					}
					if labels["queue_destination"] != "email" || sample.GetGauge().GetValue() != expected[labels["state"]] {
						t.Fatalf("invalid sample: %v", sample)
					}
				}
			}
		})
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if gather()["queue_tasks"] != nil {
		t.Fatal("cleanup retained callback")
	}
	if _, err := RegisterStats("", source, provider, time.Second); err == nil {
		t.Fatal("empty name")
	}
	if _, err := RegisterStats("email", source, provider, 0); err == nil {
		t.Fatal("zero timeout")
	}
}
