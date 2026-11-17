package benchmark

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAndResolve(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
		check   func(t *testing.T, runs []ResolvedRun)
	}{
		{
			name: "defaults are inherited and overridden",
			json: `{"name":"s","model":"m","defaults":{"prompt_words":20,"requests":5,"concurrency":1,"stream":true,"max_tokens":8,"unique_prompts":true},
			        "runs":[{"name":"a"},{"name":"b","prompt_words":40,"concurrency":4,"stream":false}]}`,
			check: func(t *testing.T, runs []ResolvedRun) {
				a, b := runs[0], runs[1]
				if a.PromptWords != 20 || a.Config.Concurrency != 1 || a.Config.Requests != 5 || !a.Config.Stream || !a.Config.UniquePrompts || a.Config.MaxTokens != 8 {
					t.Errorf("a = %+v", a)
				}
				if b.PromptWords != 40 || b.Config.Concurrency != 4 || b.Config.Requests != 5 || b.Config.Stream || !b.Config.UniquePrompts {
					t.Errorf("b = %+v", b)
				}
				if a.Config.Model != "m" || a.Config.URL != "http://fallback" {
					t.Errorf("model/url = %q %q", a.Config.Model, a.Config.URL)
				}
			},
		},
		{name: "unknown field", json: `{"name":"s","model":"m","runz":[]}`, wantErr: "unknown field"},
		{name: "bad suite name", json: `{"name":"Bad Name","model":"m","runs":[{"name":"a"}]}`, wantErr: "suite name"},
		{name: "no runs", json: `{"name":"s","model":"m"}`, wantErr: "no runs"},
		{name: "bad run name", json: `{"name":"s","model":"m","defaults":{"prompt_words":5,"requests":1,"concurrency":1},"runs":[{"name":"../x"}]}`, wantErr: "name"},
		{name: "duplicate run", json: `{"name":"s","model":"m","defaults":{"prompt_words":5,"requests":1,"concurrency":1},"runs":[{"name":"a"},{"name":"a"}]}`, wantErr: "duplicate"},
		{name: "no prompt", json: `{"name":"s","model":"m","defaults":{"requests":1,"concurrency":1},"runs":[{"name":"a"}]}`, wantErr: "exactly one"},
		{name: "both prompts", json: `{"name":"s","model":"m","defaults":{"requests":1,"concurrency":1},"runs":[{"name":"a","prompt_words":3,"prompt_file":"x"}]}`, wantErr: "exactly one"},
		{name: "zero requests", json: `{"name":"s","model":"m","defaults":{"prompt_words":5,"concurrency":1},"runs":[{"name":"a"}]}`, wantErr: "positive"},
		{name: "negative max tokens", json: `{"name":"s","model":"m","defaults":{"prompt_words":5,"requests":1,"concurrency":1,"max_tokens":-1},"runs":[{"name":"a"}]}`, wantErr: "max_tokens"},
		{name: "missing prompt file", json: `{"name":"s","model":"m","defaults":{"requests":1,"concurrency":1},"runs":[{"name":"a","prompt_file":"/nonexistent/p.txt"}]}`, wantErr: "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "suite.json", tt.json)
			s, err := LoadSuite(path)
			if err == nil {
				var runs []ResolvedRun
				runs, err = s.Resolve("", "http://fallback")
				if err == nil && tt.check != nil {
					tt.check(t, runs)
				}
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatal(err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolveModelFallback(t *testing.T) {
	s := Suite{Name: "s", Runs: []RunSpec{{Name: "a", PromptWords: 3, Concurrency: 1, Requests: 1}}}
	if _, err := s.Resolve("", "u"); err == nil {
		t.Error("want error without a model")
	}
	runs, err := s.Resolve("flag-model", "u")
	if err != nil || runs[0].Config.Model != "flag-model" {
		t.Errorf("runs = %+v, err = %v", runs, err)
	}
}

func TestGeneratePrompt(t *testing.T) {
	tests := []struct{ words int }{{1}, {25}, {1800}}
	for _, tt := range tests {
		p := GeneratePrompt(tt.words)
		if p != GeneratePrompt(tt.words) {
			t.Errorf("words=%d: not deterministic", tt.words)
		}
		intro := len(strings.Fields(GeneratePrompt(0)))
		if got := len(strings.Fields(p)) - intro; got != tt.words {
			t.Errorf("words=%d: got %d filler words", tt.words, got)
		}
	}
	if GeneratePrompt(10) == GeneratePrompt(11) {
		t.Error("different lengths produced the same prompt")
	}
}

func fakeRun(fail map[string]bool, calls *[]Config) RunFunc {
	return func(ctx context.Context, cfg Config) (Result, error) {
		*calls = append(*calls, cfg)
		sm := Sample{TTFT: time.Second, HasTTFT: true, Latency: 2 * time.Second, Tokens: 4}
		if fail[cfg.Prompt] {
			sm = Sample{Err: errors.New("boom")}
		}
		return Result{
			Model: cfg.Model, Concurrency: cfg.Concurrency, Requests: cfg.Requests, Stream: cfg.Stream,
			StartedAt: time.Unix(0, 0), Samples: []Sample{sm}, Summary: Summarize([]Sample{sm}, time.Second),
		}, nil
	}
}

func TestRunSuite(t *testing.T) {
	runs := []ResolvedRun{
		{Name: "short", PromptWords: 5, Config: Config{URL: "u", Model: "m", Prompt: "p1", Concurrency: 1, Requests: 3, Stream: true, MaxTokens: 8}},
		{Name: "long", PromptWords: 50, Config: Config{URL: "u", Model: "m", Prompt: "p2", Concurrency: 2, Requests: 3, Stream: true, MaxTokens: 8}},
	}
	dir := t.TempDir()
	var calls []Config
	var log bytes.Buffer
	got, err := RunSuite(context.Background(), fakeRun(nil, &calls), runs, dir, &log)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(calls) != 3 {
		t.Fatalf("results = %d, calls = %d, want 2 and 3 (warm-up first)", len(got), len(calls))
	}
	if w := calls[0]; w.Requests != 1 || w.Concurrency != 1 || w.MaxTokens != 1 || w.Stream {
		t.Errorf("warm-up config = %+v", w)
	}
	for _, name := range []string{"short.json", "long.json", "tables.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing output %s", name)
		}
	}
	tbl, _ := os.ReadFile(filepath.Join(dir, "tables.md"))
	if !strings.Contains(string(tbl), "| short | 5 | 1 | 1/1 |") || !strings.Contains(string(tbl), "| long | 50 | 2 |") {
		t.Errorf("tables.md = %s", tbl)
	}
}

func TestRunSuiteWarmupFailure(t *testing.T) {
	runs := []ResolvedRun{{Name: "a", Config: Config{Prompt: "p", Concurrency: 1, Requests: 1}}}
	var calls []Config
	_, err := RunSuite(context.Background(), fakeRun(map[string]bool{GeneratePrompt(10): true}, &calls), runs, t.TempDir(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "warm-up") {
		t.Fatalf("err = %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("ran %d times after a failed warm-up, want 1", len(calls))
	}
}

func TestWriteMarkdownTable(t *testing.T) {
	sm := Sample{TTFT: 1500 * time.Millisecond, HasTTFT: true, TPOT: 20 * time.Millisecond, HasTPOT: true, Latency: 3 * time.Second, Tokens: 10}
	bad := Sample{Err: errors.New("x")}
	res := Result{Concurrency: 4, Requests: 2, Samples: []Sample{sm, bad}, Summary: Summarize([]Sample{sm, bad}, 2*time.Second)}
	var b bytes.Buffer
	if err := WriteMarkdownTable(&b, []NamedResult{{Name: "r", PromptWords: 0, Result: res}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !strings.HasPrefix(lines[2], "| r | – | 4 | 1/2 | 1500 | 1500 | 20.0 | 20.0 | 3000 | 3000 | 0.50 | 5.0 |") {
		t.Errorf("row = %q", lines[2])
	}
}
