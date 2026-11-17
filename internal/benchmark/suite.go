package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RunSpec describes one run in a suite. In Suite.Defaults, zero values mean
// "unset"; in Suite.Runs, zero values inherit from the defaults.
type RunSpec struct {
	Name string `json:"name"`
	// PromptWords generates a prompt of about this many words. PromptFile reads
	// one from disk. Exactly one must be set after defaults are applied.
	PromptWords   int    `json:"prompt_words"`
	PromptFile    string `json:"prompt_file"`
	Concurrency   int    `json:"concurrency"`
	Requests      int    `json:"requests"`
	MaxTokens     int    `json:"max_tokens"`
	Stream        *bool  `json:"stream"`
	UniquePrompts *bool  `json:"unique_prompts"`
}

// Suite is a named list of runs against one model, loaded from JSON.
type Suite struct {
	Name     string    `json:"name"`
	Model    string    `json:"model"`
	URL      string    `json:"url"`
	Defaults RunSpec   `json:"defaults"`
	Runs     []RunSpec `json:"runs"`
}

// ResolvedRun is a RunSpec with defaults applied and the prompt materialized.
type ResolvedRun struct {
	Name        string
	PromptWords int // 0 when the prompt came from a file
	Config      Config
}

// NamedResult is a finished run with the context a report needs.
type NamedResult struct {
	Name        string
	PromptWords int
	Result      Result
}

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// LoadSuite reads and decodes a suite file. Unknown fields are rejected so
// typos in a config do not silently change an experiment.
func LoadSuite(path string) (Suite, error) {
	f, err := os.Open(path)
	if err != nil {
		return Suite{}, err
	}
	defer f.Close()
	var s Suite
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Suite{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// Resolve validates the suite and returns its runs ready to execute. model and
// url are fallbacks for when the suite leaves them empty.
func (s Suite) Resolve(model, url string) ([]ResolvedRun, error) {
	if !safeName.MatchString(s.Name) {
		return nil, fmt.Errorf("suite name %q must be lowercase letters, digits, '.', '_' or '-'", s.Name)
	}
	if s.Model != "" {
		model = s.Model
	}
	if s.URL != "" {
		url = s.URL
	}
	if model == "" {
		return nil, errors.New("model is required (set it in the suite or pass --model)")
	}
	if len(s.Runs) == 0 {
		return nil, errors.New("suite has no runs")
	}

	seen := map[string]bool{}
	out := make([]ResolvedRun, 0, len(s.Runs))
	for i, r := range s.Runs {
		if !safeName.MatchString(r.Name) {
			return nil, fmt.Errorf("run %d: name %q must be lowercase letters, digits, '.', '_' or '-'", i, r.Name)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("duplicate run name %q", r.Name)
		}
		seen[r.Name] = true

		m := s.merge(r)
		if m.Concurrency < 1 || m.Requests < 1 {
			return nil, fmt.Errorf("run %q: concurrency and requests must be positive", r.Name)
		}
		if m.MaxTokens < 0 {
			return nil, fmt.Errorf("run %q: max_tokens must not be negative", r.Name)
		}
		if (m.PromptWords > 0) == (m.PromptFile != "") {
			return nil, fmt.Errorf("run %q: set exactly one of prompt_words and prompt_file", r.Name)
		}

		prompt, words := "", m.PromptWords
		if words > 0 {
			prompt = GeneratePrompt(words)
		} else {
			raw, err := os.ReadFile(m.PromptFile)
			if err != nil {
				return nil, fmt.Errorf("run %q: %w", r.Name, err)
			}
			if prompt = strings.TrimSpace(string(raw)); prompt == "" {
				return nil, fmt.Errorf("run %q: prompt file %s is empty", r.Name, m.PromptFile)
			}
		}
		out = append(out, ResolvedRun{Name: r.Name, PromptWords: words, Config: Config{
			URL: url, Model: model, Prompt: prompt,
			Concurrency: m.Concurrency, Requests: m.Requests, MaxTokens: m.MaxTokens,
			Stream:        m.Stream != nil && *m.Stream,
			UniquePrompts: m.UniquePrompts != nil && *m.UniquePrompts,
		}})
	}
	return out, nil
}

// merge overlays run r on the suite defaults. Setting a prompt source on the
// run replaces the default's source rather than conflicting with it.
func (s Suite) merge(r RunSpec) RunSpec {
	m := s.Defaults
	if r.PromptWords > 0 || r.PromptFile != "" {
		m.PromptWords, m.PromptFile = r.PromptWords, r.PromptFile
	}
	if r.Concurrency != 0 {
		m.Concurrency = r.Concurrency
	}
	if r.Requests != 0 {
		m.Requests = r.Requests
	}
	if r.MaxTokens != 0 {
		m.MaxTokens = r.MaxTokens
	}
	if r.Stream != nil {
		m.Stream = r.Stream
	}
	if r.UniquePrompts != nil {
		m.UniquePrompts = r.UniquePrompts
	}
	return m
}

// RunFunc executes one benchmark run; benchmark.Run satisfies it.
type RunFunc func(ctx context.Context, cfg Config) (Result, error)

// RunSuite executes runs in order after one untimed warm-up request, so model
// load time stays out of the first measurement. Each result is saved as
// <dir>/<run name>.json and the markdown table as <dir>/tables.md. If ctx is
// cancelled it stops and returns what finished along with ctx.Err().
func RunSuite(ctx context.Context, run RunFunc, runs []ResolvedRun, dir string, log io.Writer) ([]NamedResult, error) {
	if len(runs) == 0 {
		return nil, errors.New("no runs")
	}
	warm := runs[0].Config
	warm.Concurrency, warm.Requests, warm.MaxTokens, warm.Stream, warm.UniquePrompts = 1, 1, 1, false, false
	warm.Prompt = GeneratePrompt(10)
	fmt.Fprintln(log, "warm-up request")
	if res, err := run(ctx, warm); err != nil {
		return nil, err
	} else if res.Summary.Succeeded == 0 {
		return nil, fmt.Errorf("warm-up request failed: %v", firstSampleErr(res))
	}

	var done []NamedResult
	for _, r := range runs {
		fmt.Fprintf(log, "run %s (concurrency=%d requests=%d)\n", r.Name, r.Config.Concurrency, r.Config.Requests)
		res, err := run(ctx, r.Config)
		if len(res.Samples) > 0 {
			if werr := WriteJSONTo(filepath.Join(dir, r.Name+".json"), res); werr != nil {
				return done, werr
			}
			done = append(done, NamedResult{Name: r.Name, PromptWords: r.PromptWords, Result: res})
		}
		if err != nil {
			return done, err
		}
	}

	f, err := os.Create(filepath.Join(dir, "tables.md"))
	if err != nil {
		return done, err
	}
	defer f.Close()
	if err := WriteMarkdownTable(f, done); err != nil {
		return done, err
	}
	return done, f.Close()
}

func firstSampleErr(r Result) error {
	for _, s := range r.Samples {
		if s.Err != nil {
			return s.Err
		}
	}
	return nil
}

// promptWords is the vocabulary for generated prompts.
var promptWords = strings.Fields(`river lantern copper meadow signal harbor violet engine
orchard compass marble thunder paper window garden anchor velvet canyon ribbon
summit pebble forest mirror candle bridge silver pocket rhythm island ladder
amber quartz whisper harvest tunnel feather button castle ocean saddle timber
horizon puzzle blanket glacier cobalt market shadow kettle voyage prairie`)

// GeneratePrompt returns a deterministic prompt of about words words: a short
// instruction followed by pseudo-random filler. Determinism keeps reruns
// comparable; the shuffle keeps the text from being one repeated phrase.
func GeneratePrompt(words int) string {
	const intro = "Read the following list of words, then write a long, detailed story that uses as many of them as you can. "
	var b strings.Builder
	b.WriteString(intro)
	x := uint32(12345)
	for i := 0; i < words; i++ {
		x = x*1664525 + 1013904223 // LCG, fixed seed
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(promptWords[int(x>>16)%len(promptWords)])
	}
	return b.String()
}

// WriteMarkdownTable writes one row per result with latency percentiles in
// milliseconds. The ok column is succeeded/attempted so failures are visible.
func WriteMarkdownTable(w io.Writer, results []NamedResult) error {
	fmt.Fprintln(w, "| Run | Prompt words | Concurrency | OK | TTFT P50 (ms) | TTFT P95 (ms) | TPOT P50 (ms) | TPOT P95 (ms) | Latency P50 (ms) | Latency P95 (ms) | req/s | tok/s |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, nr := range results {
		r, s := nr.Result, nr.Result.Summary
		words := "–"
		if nr.PromptWords > 0 {
			words = fmt.Sprint(nr.PromptWords)
		}
		_, err := fmt.Fprintf(w, "| %s | %s | %d | %d/%d | %.0f | %.0f | %.1f | %.1f | %.0f | %.0f | %.2f | %.1f |\n",
			nr.Name, words, r.Concurrency, s.Succeeded, s.Requests,
			msf(s.TTFT.P50), msf(s.TTFT.P95), msf(s.TPOT.P50), msf(s.TPOT.P95),
			msf(s.Latency.P50), msf(s.Latency.P95), s.Throughput, s.TokensPerSec)
		if err != nil {
			return err
		}
	}
	return nil
}
