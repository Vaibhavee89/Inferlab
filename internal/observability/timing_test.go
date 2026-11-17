package observability

import (
	"testing"
	"time"
)

func TestTimingDerived(t *testing.T) {
	start := time.Unix(1000, 0)
	at := func(d time.Duration) time.Time { return start.Add(d) }

	tests := []struct {
		name               string
		timing             Timing
		wantTTFT, wantTPOT time.Duration
		okTTFT, okTPOT     bool
		wantTotal          time.Duration
	}{
		{
			name:      "no tokens",
			timing:    Timing{Start: start, End: at(time.Second)},
			wantTotal: time.Second,
		},
		{
			name:     "one token has TTFT but no TPOT",
			timing:   Timing{Start: start, FirstToken: at(200 * time.Millisecond), End: at(time.Second), Tokens: 1},
			wantTTFT: 200 * time.Millisecond, okTTFT: true,
			wantTotal: time.Second,
		},
		{
			name:     "two tokens",
			timing:   Timing{Start: start, FirstToken: at(100 * time.Millisecond), End: at(300 * time.Millisecond), Tokens: 2},
			wantTTFT: 100 * time.Millisecond, okTTFT: true,
			wantTPOT: 200 * time.Millisecond, okTPOT: true,
			wantTotal: 300 * time.Millisecond,
		},
		{
			name:     "many tokens",
			timing:   Timing{Start: start, FirstToken: at(500 * time.Millisecond), End: at(5500 * time.Millisecond), Tokens: 101},
			wantTTFT: 500 * time.Millisecond, okTTFT: true,
			wantTPOT: 50 * time.Millisecond, okTPOT: true,
			wantTotal: 5500 * time.Millisecond,
		},
		{
			name:     "instant response",
			timing:   Timing{Start: start, FirstToken: start, End: start, Tokens: 3},
			wantTTFT: 0, okTTFT: true,
			wantTPOT: 0, okTPOT: true,
			wantTotal: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttft, ok := tt.timing.TTFT()
			if ttft != tt.wantTTFT || ok != tt.okTTFT {
				t.Errorf("TTFT = %v, %v; want %v, %v", ttft, ok, tt.wantTTFT, tt.okTTFT)
			}
			tpot, ok := tt.timing.TPOT()
			if tpot != tt.wantTPOT || ok != tt.okTPOT {
				t.Errorf("TPOT = %v, %v; want %v, %v", tpot, ok, tt.wantTPOT, tt.okTPOT)
			}
			if got := tt.timing.Total(); got != tt.wantTotal {
				t.Errorf("Total = %v, want %v", got, tt.wantTotal)
			}
		})
	}
}

func TestTracker(t *testing.T) {
	// Each clock read advances 30ms. The tracker reads the clock three times at
	// most: at Start, on the first token only, and at Finish.
	tests := []struct {
		name       string
		tokens     int
		wantTTFT   time.Duration
		okTTFT     bool
		wantTPOT   time.Duration
		okTPOT     bool
		wantTotal  time.Duration
		wantTokens int
	}{
		{"no tokens", 0, 0, false, 0, false, 30 * time.Millisecond, 0},
		{"one token", 1, 30 * time.Millisecond, true, 0, false, 60 * time.Millisecond, 1},
		// Total 60ms, first token at 30ms: (60-30)ms / 3 gaps = 10ms.
		{"four tokens", 4, 30 * time.Millisecond, true, 10 * time.Millisecond, true, 60 * time.Millisecond, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := time.Unix(0, 0)
			clock := func() time.Time {
				cur = cur.Add(30 * time.Millisecond)
				return cur
			}
			k := NewTracker(clock)
			for range tt.tokens {
				k.OnToken()
			}
			got := k.Finish()

			if got.Tokens != tt.wantTokens {
				t.Errorf("Tokens = %d, want %d", got.Tokens, tt.wantTokens)
			}
			if d, ok := got.TTFT(); d != tt.wantTTFT || ok != tt.okTTFT {
				t.Errorf("TTFT = %v, %v; want %v, %v", d, ok, tt.wantTTFT, tt.okTTFT)
			}
			if d, ok := got.TPOT(); d != tt.wantTPOT || ok != tt.okTPOT {
				t.Errorf("TPOT = %v, %v; want %v, %v", d, ok, tt.wantTPOT, tt.okTPOT)
			}
			if d := got.Total(); d != tt.wantTotal {
				t.Errorf("Total = %v, want %v", d, tt.wantTotal)
			}
		})
	}
}
