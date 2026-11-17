package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vaibhavee89/inferlab/internal/gateway"
	"github.com/Vaibhavee89/inferlab/internal/inference/fake"
)

func TestRunFlagErrors(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no model", []string{"--prompt-file", empty}, "--model is required"},
		{"no prompt file", []string{"--model", "m"}, "--prompt-file is required"},
		{"bad concurrency", []string{"--model", "m", "--prompt-file", empty, "--concurrency", "0"}, "must be positive"},
		{"missing file", []string{"--model", "m", "--prompt-file", filepath.Join(dir, "nope")}, "no such file"},
		{"empty prompt", []string{"--model", "m", "--prompt-file", empty}, "is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRunEndToEnd(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(gateway.NewRouter(logger, &fake.Provider{Tokens: []string{"a", "b", "c"}}))
	defer srv.Close()

	dir := t.TempDir()
	prompt := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(prompt, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")

	var buf bytes.Buffer
	args := []string{"--url", srv.URL, "--model", "m", "--prompt-file", prompt,
		"--concurrency", "2", "--requests", "6", "--stream", "--out", out}
	if err := run(args, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "P99") {
		t.Errorf("table missing:\n%s", buf.String())
	}
	files, err := filepath.Glob(filepath.Join(out, "bench-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("result files = %v, err = %v", files, err)
	}
}

func TestRunAllFail(t *testing.T) {
	dir := t.TempDir()
	prompt := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(prompt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1.
	args := []string{"--url", "http://127.0.0.1:1", "--model", "m", "--prompt-file", prompt,
		"--requests", "2", "--out", filepath.Join(dir, "r")}
	err := run(args, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "all 2 requests failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunSuiteEndToEnd(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(gateway.NewRouter(logger, &fake.Provider{Tokens: []string{"a", "b", "c"}}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := filepath.Join(dir, "suite.json")
	suite := `{"name":"demo","model":"m","url":"` + srv.URL + `",
		"defaults":{"prompt_words":10,"requests":4,"concurrency":1,"stream":true,"max_tokens":4,"unique_prompts":true},
		"runs":[{"name":"seq"},{"name":"par","concurrency":2}]}`
	if err := os.WriteFile(cfg, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")

	var buf bytes.Buffer
	if err := run([]string{"--config", cfg, "--out", out}, &buf); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seq.json", "par.json", "tables.md"} {
		if _, err := os.Stat(filepath.Join(out, "demo", name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	if !strings.Contains(buf.String(), "| seq |") || !strings.Contains(buf.String(), "| par |") {
		t.Errorf("table missing:\n%s", buf.String())
	}
}

func TestRunSuiteErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"name":"s","nope":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing file", []string{"--config", filepath.Join(dir, "none.json")}, "no such file"},
		{"unknown field", []string{"--config", bad}, "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
