package benchmark

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleResult() Result {
	samples := []Sample{
		{Latency: ms(100), TTFT: ms(20), HasTTFT: true, TPOT: ms(5), HasTPOT: true, Tokens: 10},
		{Latency: ms(200), Err: errors.New("boom")},
	}
	return Result{
		URL: "http://x", Model: "m", Concurrency: 2, Requests: 2, Stream: true,
		StartedAt: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
		Samples:   samples, Summary: Summarize(samples, time.Second),
	}
}

func TestWriteTable(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, sampleResult()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"P50", "P95", "P99", "TTFT", "TPOT", "latency", "failed=1", "throughput=1.00 req/s", "mode=stream"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "results") // must be created
	path, err := WriteJSON(dir, sampleResult())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "bench-") {
		t.Errorf("unexpected path %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Config.Model != "m" || !got.Config.Stream || got.Summary.Failed != 1 {
		t.Errorf("config/summary wrong: %+v", got)
	}
	if got.Summary.LatencyMs.P50 != 100 || got.Summary.Throughput != 1 {
		t.Errorf("summary wrong: %+v", got.Summary)
	}
	if len(got.Samples) != 2 || got.Samples[0].TTFTMs == nil || *got.Samples[0].TTFTMs != 20 {
		t.Errorf("samples wrong: %+v", got.Samples)
	}
	if got.Samples[1].TTFTMs != nil || got.Samples[1].Error != "boom" {
		t.Errorf("failed sample wrong: %+v", got.Samples[1])
	}
}
