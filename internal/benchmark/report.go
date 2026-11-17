package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"
)

func msf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// WriteTable prints a human-readable summary of r to w.
func WriteTable(w io.Writer, r Result) error {
	s := r.Summary
	mode := "non-stream"
	if r.Stream {
		mode = "stream"
	}
	fmt.Fprintf(w, "target=%s model=%s mode=%s concurrency=%d\n", r.URL, r.Model, mode, r.Concurrency)
	fmt.Fprintf(w, "requests=%d ok=%d failed=%d duration=%.2fs throughput=%.2f req/s tokens=%.2f tok/s\n\n",
		s.Requests, s.Succeeded, s.Failed, s.Duration.Seconds(), s.Throughput, s.TokensPerSec)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "metric (ms)\tP50\tP95\tP99\t")
	row := func(name string, p Percentiles) {
		fmt.Fprintf(tw, "%s\t%.1f\t%.1f\t%.1f\t\n", name, msf(p.P50), msf(p.P95), msf(p.P99))
	}
	row("TTFT", s.TTFT)
	row("TPOT", s.TPOT)
	row("latency", s.Latency)
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\nTTFT is measured client-side (includes network). TPOT is reported for streaming runs only.")
	return err
}

type jsonPercentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type jsonSample struct {
	TTFTMs    *float64 `json:"ttft_ms,omitempty"`
	TPOTMs    *float64 `json:"tpot_ms,omitempty"`
	LatencyMs float64  `json:"latency_ms"`
	Tokens    int      `json:"tokens"`
	Error     string   `json:"error,omitempty"`
}

type jsonReport struct {
	StartedAt time.Time `json:"started_at"`
	Config    struct {
		URL           string `json:"url"`
		Model         string `json:"model"`
		Concurrency   int    `json:"concurrency"`
		Requests      int    `json:"requests"`
		Stream        bool   `json:"stream"`
		MaxTokens     int    `json:"max_tokens,omitempty"`
		UniquePrompts bool   `json:"unique_prompts,omitempty"`
	} `json:"config"`
	Summary struct {
		Requests     int             `json:"requests"`
		Succeeded    int             `json:"succeeded"`
		Failed       int             `json:"failed"`
		DurationSec  float64         `json:"duration_sec"`
		Throughput   float64         `json:"throughput_rps"`
		TokensPerSec float64         `json:"tokens_per_sec"`
		TTFTMs       jsonPercentiles `json:"ttft_ms"`
		TPOTMs       jsonPercentiles `json:"tpot_ms"`
		LatencyMs    jsonPercentiles `json:"latency_ms"`
	} `json:"summary"`
	Samples []jsonSample `json:"samples"`
}

func jp(p Percentiles) jsonPercentiles {
	return jsonPercentiles{msf(p.P50), msf(p.P95), msf(p.P99)}
}

// encodeJSON renders r as the indented JSON report. Durations are
// floating-point milliseconds.
func encodeJSON(r Result) ([]byte, error) {
	var rep jsonReport
	rep.StartedAt = r.StartedAt
	rep.Config.URL, rep.Config.Model = r.URL, r.Model
	rep.Config.Concurrency, rep.Config.Requests, rep.Config.Stream = r.Concurrency, r.Requests, r.Stream
	rep.Config.MaxTokens, rep.Config.UniquePrompts = r.MaxTokens, r.UniquePrompts
	s := r.Summary
	rep.Summary.Requests, rep.Summary.Succeeded, rep.Summary.Failed = s.Requests, s.Succeeded, s.Failed
	rep.Summary.DurationSec = s.Duration.Seconds()
	rep.Summary.Throughput, rep.Summary.TokensPerSec = s.Throughput, s.TokensPerSec
	rep.Summary.TTFTMs, rep.Summary.TPOTMs, rep.Summary.LatencyMs = jp(s.TTFT), jp(s.TPOT), jp(s.Latency)

	rep.Samples = make([]jsonSample, 0, len(r.Samples))
	for _, sm := range r.Samples {
		js := jsonSample{LatencyMs: msf(sm.Latency), Tokens: sm.Tokens}
		if sm.HasTTFT {
			v := msf(sm.TTFT)
			js.TTFTMs = &v
		}
		if sm.HasTPOT {
			v := msf(sm.TPOT)
			js.TPOTMs = &v
		}
		if sm.Err != nil {
			js.Error = sm.Err.Error()
		}
		rep.Samples = append(rep.Samples, js)
	}

	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteJSON saves r as bench-<UTC timestamp>.json in dir, creating dir if
// needed, and returns the file path. Durations are floating-point milliseconds.
func WriteJSON(dir string, r Result) (string, error) {
	name := "bench-" + r.StartedAt.UTC().Format("20060102T150405.000Z") + ".json"
	path := filepath.Join(dir, name)
	return path, WriteJSONTo(path, r)
}

// WriteJSONTo saves r at exactly path, creating parent directories. Suites use
// it for stable file names so reruns overwrite earlier results.
func WriteJSONTo(path string, r Result) error {
	data, err := encodeJSON(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
