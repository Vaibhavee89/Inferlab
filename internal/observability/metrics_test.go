package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// sampleCounts returns the histogram sample count (or counter value) per
// metric family name, summed over all label sets.
func sampleCounts(t *testing.T, reg *prometheus.Registry) map[string]uint64 {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]uint64{}
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			switch {
			case m.GetHistogram() != nil:
				out[f.GetName()] += m.GetHistogram().GetSampleCount()
			case m.GetCounter() != nil:
				out[f.GetName()] += uint64(m.GetCounter().GetValue())
			}
		}
	}
	return out
}

func TestMetricsObserve(t *testing.T) {
	start := time.Unix(0, 0)
	multi := Timing{Start: start, FirstToken: start.Add(time.Second), End: start.Add(3 * time.Second), Tokens: 5}
	single := Timing{Start: start, FirstToken: start.Add(time.Second), End: start.Add(2 * time.Second), Tokens: 1}
	empty := Timing{Start: start, End: start.Add(time.Second)}

	tests := []struct {
		name   string
		timing Timing
		want   map[string]uint64
	}{
		{"multi token", multi, map[string]uint64{
			"inferlab_ttft_seconds": 1, "inferlab_tpot_seconds": 1,
			"inferlab_request_duration_seconds": 1, "inferlab_output_tokens": 1, "inferlab_requests_total": 1,
		}},
		{"single token has no TPOT", single, map[string]uint64{
			"inferlab_ttft_seconds": 1, "inferlab_tpot_seconds": 0,
			"inferlab_request_duration_seconds": 1, "inferlab_output_tokens": 1, "inferlab_requests_total": 1,
		}},
		{"no tokens has no TTFT or TPOT", empty, map[string]uint64{
			"inferlab_ttft_seconds": 0, "inferlab_tpot_seconds": 0,
			"inferlab_request_duration_seconds": 1, "inferlab_output_tokens": 1, "inferlab_requests_total": 1,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			NewMetrics(reg).Observe("m", true, tt.timing)
			got := sampleCounts(t, reg)
			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%s = %d, want %d", name, got[name], want)
				}
			}
		})
	}
}

func TestMetricsObserveFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	m.ObserveFailure(UnknownModel)

	got := sampleCounts(t, reg)
	if got["inferlab_requests_total"] != 1 {
		t.Errorf("requests_total = %d, want 1", got["inferlab_requests_total"])
	}
	for _, name := range []string{"inferlab_ttft_seconds", "inferlab_tpot_seconds", "inferlab_request_duration_seconds", "inferlab_output_tokens"} {
		if got[name] != 0 {
			t.Errorf("%s = %d, want 0 after a failure", name, got[name])
		}
	}
}

func TestMetricsTrackInFlight(t *testing.T) {
	tests := []struct {
		name    string
		started int
		done    int
		want    float64
	}{
		{"none", 0, 0, 0},
		{"two in flight", 2, 0, 2},
		{"one finished", 2, 1, 1},
		{"all finished", 3, 3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m := NewMetrics(reg)
			var dones []func()
			for i := 0; i < tt.started; i++ {
				dones = append(dones, m.TrackInFlight())
			}
			for i := 0; i < tt.done; i++ {
				dones[i]()
			}
			fams, err := reg.Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range fams {
				if f.GetName() == "inferlab_inflight_requests" {
					if got := f.GetMetric()[0].GetGauge().GetValue(); got != tt.want {
						t.Errorf("inflight = %v, want %v", got, tt.want)
					}
					return
				}
			}
			t.Fatal("inferlab_inflight_requests not registered")
		})
	}
}
