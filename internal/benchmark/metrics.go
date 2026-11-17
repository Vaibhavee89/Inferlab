// Package benchmark fires concurrent requests at an OpenAI-compatible endpoint
// and summarizes latency and throughput.
package benchmark

import (
	"math"
	"sort"
	"time"
)

// Sample is the measurement of one request.
type Sample struct {
	// TTFT is the time to first token, measured client-side. Valid if HasTTFT.
	TTFT    time.Duration
	HasTTFT bool
	// TPOT is the mean time per output token after the first. Valid if HasTPOT.
	TPOT    time.Duration
	HasTPOT bool
	// Latency is the end-to-end request time.
	Latency time.Duration
	// Tokens is the number of content chunks received.
	Tokens int
	// Err is non-nil if the request failed.
	Err error
}

// Percentiles holds the latency distribution points we report.
type Percentiles struct {
	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
}

// Summary aggregates a run. Percentiles cover successful requests only.
type Summary struct {
	Requests  int
	Succeeded int
	Failed    int
	// Duration is the wall-clock time of the whole run.
	Duration time.Duration
	// Throughput is successful requests per second.
	Throughput float64
	// TokensPerSec is total output tokens per second across all workers.
	TokensPerSec float64
	TTFT         Percentiles
	TPOT         Percentiles
	Latency      Percentiles
}

// Summarize computes a Summary from samples and the run's wall-clock time.
func Summarize(samples []Sample, wall time.Duration) Summary {
	s := Summary{Requests: len(samples), Duration: wall}
	var ttft, tpot, lat []time.Duration
	tokens := 0
	for _, sm := range samples {
		if sm.Err != nil {
			s.Failed++
			continue
		}
		s.Succeeded++
		tokens += sm.Tokens
		lat = append(lat, sm.Latency)
		if sm.HasTTFT {
			ttft = append(ttft, sm.TTFT)
		}
		if sm.HasTPOT {
			tpot = append(tpot, sm.TPOT)
		}
	}
	if wall > 0 {
		s.Throughput = float64(s.Succeeded) / wall.Seconds()
		s.TokensPerSec = float64(tokens) / wall.Seconds()
	}
	s.TTFT = percentiles(ttft)
	s.TPOT = percentiles(tpot)
	s.Latency = percentiles(lat)
	return s
}

func percentiles(d []time.Duration) Percentiles {
	if len(d) == 0 {
		return Percentiles{}
	}
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return Percentiles{
		P50: nearestRank(sorted, 50),
		P95: nearestRank(sorted, 95),
		P99: nearestRank(sorted, 99),
	}
}

// nearestRank returns the p-th percentile of an ascending slice using the
// nearest-rank method: the value at rank ceil(p/100 * n).
func nearestRank(sorted []time.Duration, p float64) time.Duration {
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}
