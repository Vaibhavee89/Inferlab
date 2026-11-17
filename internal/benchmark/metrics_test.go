package benchmark

import (
	"errors"
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestNearestRank(t *testing.T) {
	seq := func(n int) []time.Duration {
		d := make([]time.Duration, n)
		for i := range d {
			d[i] = ms(i + 1)
		}
		return d
	}
	tests := []struct {
		name string
		in   []time.Duration
		want Percentiles
	}{
		{"empty", nil, Percentiles{}},
		{"single", seq(1), Percentiles{ms(1), ms(1), ms(1)}},
		{"ten", seq(10), Percentiles{ms(5), ms(10), ms(10)}},
		{"hundred", seq(100), Percentiles{ms(50), ms(95), ms(99)}},
		{"unsorted", []time.Duration{ms(30), ms(10), ms(20)}, Percentiles{ms(20), ms(30), ms(30)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := percentiles(tt.in); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	ok := func(lat int, tokens int) Sample {
		return Sample{Latency: ms(lat), TTFT: ms(lat / 2), HasTTFT: true, TPOT: ms(1), HasTPOT: tokens > 1, Tokens: tokens}
	}
	tests := []struct {
		name    string
		samples []Sample
		wall    time.Duration
		check   func(t *testing.T, s Summary)
	}{
		{
			name: "empty", wall: time.Second,
			check: func(t *testing.T, s Summary) {
				if s.Requests != 0 || s.Throughput != 0 || s.Latency != (Percentiles{}) {
					t.Errorf("unexpected %+v", s)
				}
			},
		},
		{
			name:    "failures excluded from percentiles",
			samples: []Sample{ok(100, 10), ok(200, 10), {Latency: ms(9999), Err: errors.New("boom")}},
			wall:    2 * time.Second,
			check: func(t *testing.T, s Summary) {
				if s.Requests != 3 || s.Succeeded != 2 || s.Failed != 1 {
					t.Errorf("counts: %+v", s)
				}
				if s.Latency.P99 != ms(200) {
					t.Errorf("P99 = %v, want 200ms", s.Latency.P99)
				}
				if s.Throughput != 1 {
					t.Errorf("throughput = %v, want 1 req/s", s.Throughput)
				}
				if s.TokensPerSec != 10 {
					t.Errorf("tokens/s = %v, want 10", s.TokensPerSec)
				}
			},
		},
		{
			name:    "samples without TPOT skipped",
			samples: []Sample{ok(100, 1), ok(100, 5)},
			wall:    time.Second,
			check: func(t *testing.T, s Summary) {
				if s.TPOT.P50 != ms(1) {
					t.Errorf("TPOT P50 = %v, want 1ms", s.TPOT.P50)
				}
			},
		},
		{
			name:    "zero wall avoids division",
			samples: []Sample{ok(10, 2)},
			wall:    0,
			check: func(t *testing.T, s Summary) {
				if s.Throughput != 0 || s.TokensPerSec != 0 {
					t.Errorf("unexpected %+v", s)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, Summarize(tt.samples, tt.wall)) })
	}
}
