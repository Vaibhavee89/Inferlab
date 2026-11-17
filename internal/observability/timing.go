// Package observability measures and exports inference performance metrics.
package observability

import "time"

// Timing is the raw measurement of one generation.
type Timing struct {
	// Start is when the request entered the gateway.
	Start time.Time
	// FirstToken is when the first non-empty token arrived; zero if none did.
	FirstToken time.Time
	// End is when the generation finished.
	End time.Time
	// Tokens is the number of output tokens. It counts non-empty stream
	// chunks, which approximates backend tokens (Ollama emits about one token
	// per chunk).
	Tokens int
}

// TTFT is the time to first token. ok is false if no token was produced.
func (t Timing) TTFT() (d time.Duration, ok bool) {
	if t.Tokens == 0 || t.FirstToken.IsZero() {
		return 0, false
	}
	return t.FirstToken.Sub(t.Start), true
}

// TPOT is the mean time per output token after the first:
// (End - FirstToken) / (Tokens - 1). ok is false for fewer than two tokens,
// where the quantity is undefined.
func (t Timing) TPOT() (d time.Duration, ok bool) {
	if t.Tokens < 2 || t.FirstToken.IsZero() {
		return 0, false
	}
	return t.End.Sub(t.FirstToken) / time.Duration(t.Tokens-1), true
}

// Total is the end-to-end latency.
func (t Timing) Total() time.Duration { return t.End.Sub(t.Start) }

// Tracker records a Timing as a generation progresses. It is not safe for
// concurrent use; one goroutine owns each request.
type Tracker struct {
	now func() time.Time
	t   Timing
}

// NewTracker starts timing now. A nil clock means time.Now.
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now, t: Timing{Start: now()}}
}

// OnToken records the arrival of one output token.
func (k *Tracker) OnToken() {
	if k.t.Tokens == 0 {
		k.t.FirstToken = k.now()
	}
	k.t.Tokens++
}

// Finish stamps the end time and returns the measurement.
func (k *Tracker) Finish() Timing {
	k.t.End = k.now()
	return k.t
}
